// Package brain models a second brain: a git repository of markdown notes that
// follow the summary-first, freshness-tracked conventions. It is the storage
// layer the agent talks to — read, write, search — while git transport stays
// hidden behind it.
package brain

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Settings is the per-brain configuration stored at <root>/.multi/brain.yaml.
type Settings struct {
	// Name is the human label for the brain.
	Name string `yaml:"name"`
	// Split lists the mutually-exclusive "half" tags every content note must
	// carry exactly one of (e.g. [domain, operations]). Empty disables the rule.
	Split []string `yaml:"split,omitempty"`
	// Taxonomy closes the type/status vocabularies. Empty lists disable the rule.
	Taxonomy Taxonomy `yaml:"taxonomy,omitempty"`
	// Wakeup lists the identity notes `multi wake-up` prints in full (L0).
	// Keep them short — they are loaded at the start of every session.
	Wakeup []string `yaml:"wakeup,omitempty"`
	// Ignore lists directory names excluded from note discovery anywhere in
	// the tree (e.g. [dist, vendor]), for brains that host tooling alongside
	// notes. Dot-directories and node_modules are always ignored.
	Ignore []string `yaml:"ignore,omitempty"`
	// Embeddings configures the optional semantic shadow index. Nil disables
	// `multi reindex` and `multi similar` for this brain.
	Embeddings *EmbedSettings `yaml:"embeddings,omitempty"`
	// FilingRules names a note whose body the ingester and groomer give the
	// agent as this brain's filing rules (layout, size, dates, MOCs), in place
	// of their built-in ones. Empty keeps the built-in rules.
	FilingRules string `yaml:"filing_rules,omitempty"`
}

// Taxonomy is the closed vocabulary for note front matter. A configured list
// makes writes reject and lint flag values outside it.
type Taxonomy struct {
	Types    []string `yaml:"types,omitempty"`
	Statuses []string `yaml:"statuses,omitempty"`
}

// CheckType returns an error when types are closed and t is not among them.
func (x Taxonomy) CheckType(t string) error {
	return checkVocab("type", t, x.Types)
}

// CheckStatus returns an error when statuses are closed and s is not among them.
func (x Taxonomy) CheckStatus(s string) error {
	return checkVocab("status", s, x.Statuses)
}

func checkVocab(field, v string, allowed []string) error {
	if len(allowed) == 0 || v == "" {
		return nil
	}
	for _, a := range allowed {
		if strings.EqualFold(a, v) {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not in the brain's taxonomy (%s)", field, v, strings.Join(allowed, "|"))
}

// Brain is an opened brain rooted at a directory on disk.
type Brain struct {
	Root     string
	Settings Settings
	// LocalEmbeddings overrides parts of Settings.Embeddings on this machine:
	// from the user's registry, never saved to the brain. Read the settings
	// through Embeddings.
	LocalEmbeddings *EmbedSettings
}

const settingsRel = ".multi/brain.yaml"

// Open loads the brain rooted at root. A missing settings file is not an error —
// the brain simply has no enforced split.
func Open(root string) (*Brain, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	b := &Brain{Root: abs}
	if data, err := os.ReadFile(filepath.Join(abs, settingsRel)); err == nil {
		_ = yaml.Unmarshal(data, &b.Settings)
	}
	return b, nil
}

// DisplayName is the brain's human label: its configured name, else the
// directory base name.
func (b *Brain) DisplayName() string {
	if b.Settings.Name != "" {
		return b.Settings.Name
	}
	return filepath.Base(b.Root)
}

// SaveSettings writes the per-brain settings file.
func (b *Brain) SaveSettings() error {
	dir := filepath.Join(b.Root, ".multi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(b.Settings)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.Root, settingsRel), data, 0o644)
}

// skipWalkDir reports whether a walked directory is pruned from note
// discovery: dot-directories (.git, .obsidian, .multi), node_modules, and any
// directory name listed under `ignore:` in the brain settings.
func (b *Brain) skipWalkDir(name string) bool {
	if strings.HasPrefix(name, ".") || name == "node_modules" {
		return true
	}
	return slices.Contains(b.Settings.Ignore, name)
}

// Notes returns every markdown note path (vault-relative, sorted), skipping
// dot-directories such as .git, .obsidian and .multi, node_modules, and the
// brain's configured ignore directories.
func (b *Brain) Notes() ([]string, error) {
	var out []string
	err := filepath.WalkDir(b.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != b.Root && b.skipWalkDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			rel, relErr := filepath.Rel(b.Root, p)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// Resolve turns a wikilink-style query (bare name, name.md, or exact relative
// path) into a single vault-relative note path.
func (b *Brain) Resolve(q string) (string, error) {
	q = filepath.ToSlash(strings.TrimSpace(q))
	if strings.HasSuffix(q, ".md") && fileExists(filepath.Join(b.Root, q)) {
		return q, nil
	}
	if fileExists(filepath.Join(b.Root, q+".md")) {
		return q + ".md", nil
	}
	notes, err := b.Notes()
	if err != nil {
		return "", err
	}
	want := strings.TrimSuffix(q, ".md")
	wantKey := slugKey(want)
	// Prefer an exact base-name match; fall back to slug-equality so a legacy
	// reference like "Formula Student" still resolves to "formula-student.md"
	// (and the reverse), keeping links live across the kebab-case migration.
	var exact, fuzzy []string
	for _, rel := range notes {
		base := strings.TrimSuffix(filepath.Base(rel), ".md")
		switch {
		case base == want:
			exact = append(exact, rel)
		case wantKey != "" && slugKey(base) == wantKey:
			fuzzy = append(fuzzy, rel)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = fuzzy
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", &NotFoundError{Query: q}
	default:
		return "", &AmbiguousError{Query: q, Matches: matches}
	}
}

// IsContent reports whether a note is a "content" note subject to the split and
// freshness rules — i.e. not a root-level navigation note, not a template, and
// not type: meta.
func (b *Brain) IsContent(n *Note) bool {
	if !strings.Contains(n.Rel, "/") {
		return false
	}
	if strings.HasPrefix(strings.ToLower(n.Rel), "templates/") {
		return false
	}
	return n.FM.Type != "meta"
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

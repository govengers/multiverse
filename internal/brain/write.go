package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrSummaryRequired enforces the brain's first standing rule: no note without a
// one-line summary. This is the guarantee that makes the brain navigable.
var ErrSummaryRequired = errors.New("a note must have a one-line summary (the gate readers read first)")

// WriteParams describes a note to create.
type WriteParams struct {
	Path      string // explicit vault-relative path; takes precedence over Dir/Title
	Dir       string // directory for the note when Path is empty
	Title     string // note title; also the filename when Path is empty
	Type      string
	Status    string
	Pinned    bool // surface this note in `multi wake-up`
	Summary   string
	Tags      []string
	Source    string
	Retrieved string
	Freshness string
	Body      string
	Force     bool // overwrite an existing note
}

// Write creates a note from params, enforcing the summary rule and auto-filling
// created/retrieved dates. It returns the note's vault-relative path.
func (b *Brain) Write(p WriteParams) (string, error) {
	if strings.TrimSpace(p.Summary) == "" {
		return "", ErrSummaryRequired
	}
	rel := notePath(p)
	if rel == "" {
		return "", errors.New("a note needs a --path or a --title")
	}
	// Taxonomy binds content notes only — root navigation notes, templates and
	// type: meta stay exempt, mirroring the split/freshness rules.
	if strings.Contains(rel, "/") && !strings.HasPrefix(strings.ToLower(rel), "templates/") && orDefault(p.Type, "reference") != "meta" {
		if err := b.Settings.Taxonomy.CheckType(orDefault(p.Type, "reference")); err != nil {
			return "", err
		}
		if err := b.Settings.Taxonomy.CheckStatus(orDefault(p.Status, "active")); err != nil {
			return "", err
		}
	}
	abs := filepath.Join(b.Root, filepath.FromSlash(rel))
	if !p.Force {
		if _, err := os.Stat(abs); err == nil {
			return "", fmt.Errorf("note already exists: %s (use --force to overwrite, or `multi append`)", rel)
		}
	}

	today := time.Now().Format("2006-01-02")
	fm := FrontMatter{
		Type:      orDefault(p.Type, "reference"),
		Status:    orDefault(p.Status, "active"),
		Pinned:    p.Pinned,
		Tags:      p.Tags,
		Created:   today,
		Summary:   strings.TrimSpace(p.Summary),
		Source:    p.Source,
		Retrieved: orDefault(p.Retrieved, today),
		Freshness: p.Freshness,
	}

	title := p.Title
	if title == "" {
		title = humanTitle(p)
	}
	body := strings.TrimLeft(p.Body, "\n")
	var sb strings.Builder
	sb.WriteString(fm.Render())
	sb.WriteString("\n# ")
	sb.WriteString(title)
	sb.WriteString("\n")
	if body != "" {
		sb.WriteString("\n")
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// Append adds content to the body of an existing note, leaving its front matter
// untouched.
func (b *Brain) Append(rel, content string) error {
	abs := filepath.Join(b.Root, filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if n := parseNote(rel, data); n.HasFM && n.FMErr != nil {
		return fmt.Errorf("%s: front matter is not valid YAML (%v); rewrite the broken value with --summary, --freshness or --retrieved", rel, n.FMErr)
	}
	buf := string(data)
	if !strings.HasSuffix(buf, "\n") {
		buf += "\n"
	}
	if !strings.HasSuffix(buf, "\n\n") {
		buf += "\n"
	}
	buf += strings.TrimRight(content, "\n") + "\n"
	return os.WriteFile(abs, []byte(buf), 0o644)
}

// UpdatableFields are the front-matter keys whose values may age after a note is
// written; every other key is fixed at creation.
var UpdatableFields = []string{"summary", "freshness", "retrieved"}

// UpdateFields replaces the values of UpdatableFields in an existing note's front
// matter, quoting them as needed, and leaves every other line of the block as it
// is. A key the note lacks is added at the end of the block. It also repairs a
// block that was invalid only because of one of these values.
func (b *Brain) UpdateFields(rel string, values map[string]string) error {
	abs := filepath.Join(b.Root, filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	n := parseNote(rel, data)
	if !n.HasFM {
		return fmt.Errorf("%s: no front matter to update", rel)
	}
	lines := strings.Split(n.RawFM, "\n")
	for _, k := range UpdatableFields {
		v, ok := values[k]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || strings.Contains(v, "\n") {
			return fmt.Errorf("%s must be a single non-empty line", k)
		}
		lines = setField(lines, k, k+": "+yamlScalar(v))
	}
	block := strings.Join(lines, "\n")
	if updated := parseNote(rel, []byte("---\n"+block+"\n---\n")); updated.FMErr != nil {
		return fmt.Errorf("%s: front matter is still not valid YAML (%v)", rel, updated.FMErr)
	}
	return os.WriteFile(abs, []byte("---\n"+block+"\n---\n"+n.Body), 0o644)
}

// setField replaces the top-level line for key, together with any indented
// continuation lines below it, or appends line when the key is absent.
func setField(lines []string, key, line string) []string {
	for i, l := range lines {
		if !strings.HasPrefix(l, key+":") {
			continue
		}
		end := i + 1
		for end < len(lines) && (strings.HasPrefix(lines[end], " ") || strings.HasPrefix(lines[end], "\t")) {
			end++
		}
		return append(append(append([]string{}, lines[:i]...), line), lines[end:]...)
	}
	return append(lines, line)
}

// notePath derives the note's vault-relative path and forces it to kebab-case so
// names stay portable across case-insensitive filesystems. A title like
// "Stalwart v0.16 / Migration" can no longer leak into directory nesting or a
// truncated filename — every segment is slugged independently.
func notePath(p WriteParams) string {
	if p.Path != "" {
		rel := filepath.ToSlash(p.Path)
		if !strings.HasSuffix(rel, ".md") {
			rel += ".md"
		}
		return SlugifyPath(rel)
	}
	if p.Title == "" {
		return ""
	}
	name := slugOrUntitled(p.Title) + ".md"
	if p.Dir != "" {
		return SlugifyPath(p.Dir) + "/" + name
	}
	return name
}

// humanTitle is the H1 heading used when no explicit --title is given: the
// original (un-slugged) base name of an explicit --path, so the note body stays
// human-readable even though its filename is a slug.
func humanTitle(p WriteParams) string {
	src := p.Path
	if src == "" {
		src = p.Title
	}
	base := filepath.Base(filepath.ToSlash(src))
	return strings.TrimSuffix(base, ".md")
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

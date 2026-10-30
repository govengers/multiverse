package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mxcd/multiverse/internal/brain"
	"github.com/mxcd/multiverse/internal/config"
	"github.com/urfave/cli/v3"
)

// ScopedBrain is an opened brain with its display name in the current scope.
type ScopedBrain struct {
	*brain.Brain
	Name    string
	Aliases []string // registry aliases, also accepted as a brain:note qualifier
}

// Scope is the set of brains resolved for the current working context: which
// brains to read from (Sources) and which to write to (Targets).
type Scope struct {
	Sources []ScopedBrain
	Targets []ScopedBrain
	Origin  string // human description of where the scope came from
}

// resolveScope determines the brains in play, in precedence order:
//  1. --brain flag (one brain, both source and target)
//  2. nearest .multi.yaml up the directory tree (the first-class binding)
//  3. the brain the cwd sits inside
//  4. the active brain in the registry
func resolveScope(cmd *cli.Command) (*Scope, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if v := cmd.String("brain"); v != "" {
		sb, err := openRef(cfg, v)
		if err != nil {
			return nil, err
		}
		return &Scope{Sources: []ScopedBrain{sb}, Targets: []ScopedBrain{sb}, Origin: "--brain " + v}, nil
	}

	if path, bnd, err := config.FindBinding(); err != nil {
		return nil, err
	} else if bnd != nil {
		return buildScope(cfg, bnd, path)
	}

	if root := findBrainRoot(); root != "" {
		b, err := brain.Open(root)
		if err != nil {
			return nil, err
		}
		b.LocalEmbeddings = cfg.LocalEmbeddings(cfg.FindPath(root))
		sb := ScopedBrain{Brain: b, Name: b.DisplayName()}
		return &Scope{Sources: []ScopedBrain{sb}, Targets: []ScopedBrain{sb}, Origin: root}, nil
	}

	if ab := cfg.ActiveBrain(); ab != nil {
		sb, err := openRef(cfg, ab.Name)
		if err != nil {
			return nil, err
		}
		return &Scope{Sources: []ScopedBrain{sb}, Targets: []ScopedBrain{sb}, Origin: "active brain"}, nil
	}

	return nil, errors.New("no brain in scope: bind this directory with `multi use <brain>...`, " +
		"pass --brain, cd into a brain, or set an active brain with `multi brain use`")
}

func buildScope(cfg *config.Config, bnd *config.Binding, origin string) (*Scope, error) {
	if len(bnd.Sources) == 0 && len(bnd.Targets) == 0 {
		return nil, fmt.Errorf("%s declares no sources or targets", origin)
	}
	s := &Scope{Origin: origin}
	for _, ref := range bnd.Sources {
		sb, err := openRef(cfg, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", origin, err)
		}
		s.Sources = append(s.Sources, sb)
	}
	if !bnd.ReadOnly {
		targets := bnd.Targets
		if len(targets) == 0 {
			targets = bnd.Sources // default: what you can read, you can write
		}
		for _, ref := range targets {
			sb, err := openRef(cfg, ref)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", origin, err)
			}
			s.Targets = append(s.Targets, sb)
		}
	}
	return s, nil
}

// openRef resolves a brain reference (registry name, alias or directory path) to a brain.
func openRef(cfg *config.Config, ref string) (ScopedBrain, error) {
	if entry := cfg.Find(ref); entry != nil {
		b, err := brain.Open(entry.Path)
		if err != nil {
			return ScopedBrain{}, err
		}
		b.LocalEmbeddings = cfg.LocalEmbeddings(entry)
		return ScopedBrain{Brain: b, Name: entry.Name, Aliases: entry.Aliases}, nil
	}
	if isDir(ref) {
		b, err := brain.Open(ref)
		if err != nil {
			return ScopedBrain{}, err
		}
		b.LocalEmbeddings = cfg.LocalEmbeddings(cfg.FindPath(ref))
		return ScopedBrain{Brain: b, Name: b.DisplayName()}, nil
	}
	return ScopedBrain{}, fmt.Errorf("unknown brain %q (not registered, not a directory) — try `multi brain list` or `multi clone`", ref)
}

// multiSource reports whether the scope reads from more than one brain.
func (s *Scope) multiSource() bool { return len(s.Sources) > 1 }

func (s *Scope) sourceNames() string {
	var names []string
	for _, sb := range s.Sources {
		names = append(names, sb.Name)
	}
	return strings.Join(names, ", ")
}

// resolveNote finds a note across the scope's source brains. A "brain:note"
// prefix targets a specific source; otherwise a name matching in exactly one
// brain wins and cross-brain matches are reported as ambiguous.
func (s *Scope) resolveNote(ref string) (ScopedBrain, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ScopedBrain{}, "", errors.New("a note name or path is required")
	}
	if i := strings.Index(ref, ":"); i > 0 {
		prefix := ref[:i]
		for _, sb := range s.Sources {
			if sb.Name == prefix || slices.Contains(sb.Aliases, prefix) {
				rel, err := sb.Resolve(ref[i+1:])
				return sb, rel, err
			}
		}
	}
	type hit struct {
		sb  ScopedBrain
		rel string
	}
	var hits []hit
	for _, sb := range s.Sources {
		if rel, err := sb.Resolve(ref); err == nil {
			hits = append(hits, hit{sb, rel})
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].sb, hits[0].rel, nil
	case 0:
		return ScopedBrain{}, "", fmt.Errorf("no note matches %q in scope (sources: %s)", ref, s.sourceNames())
	default:
		var opts []string
		for _, h := range hits {
			opts = append(opts, h.sb.Name+":"+h.rel)
		}
		return ScopedBrain{}, "", fmt.Errorf("%q matches multiple brains: %s — qualify with brain:note", ref, strings.Join(opts, ", "))
	}
}

// writeTarget chooses the brain a write lands in: the first target by default,
// overridable with --brain (which makes Targets a single brain upstream).
func (s *Scope) writeTarget() (ScopedBrain, error) {
	if len(s.Targets) == 0 {
		return ScopedBrain{}, errors.New("no write target in scope: set one with `multi scope set --target <brain>` or pass --brain")
	}
	return s.Targets[0], nil
}

// union returns sources and targets de-duplicated by root — the brains a
// scope-wide operation (sync, status, lint) should touch.
func (s *Scope) union() []ScopedBrain {
	seen := map[string]bool{}
	var out []ScopedBrain
	for _, sb := range append(append([]ScopedBrain{}, s.Sources...), s.Targets...) {
		if !seen[sb.Root] {
			seen[sb.Root] = true
			out = append(out, sb)
		}
	}
	return out
}

func (s *Scope) index() ([]brain.NoteInfo, error) {
	var all []brain.NoteInfo
	for _, sb := range s.Sources {
		notes, err := sb.Index()
		if err != nil {
			return nil, err
		}
		all = append(all, stamp(notes, sb.Name)...)
	}
	return all, nil
}

func (s *Scope) search(q string, body bool) ([]brain.NoteInfo, error) {
	var all []brain.NoteInfo
	for _, sb := range s.Sources {
		notes, err := sb.Search(q, body)
		if err != nil {
			return nil, err
		}
		all = append(all, stamp(notes, sb.Name)...)
	}
	brain.SortByScore(all)
	return all, nil
}

func (s *Scope) find(f brain.FindFilter) ([]brain.NoteInfo, error) {
	var all []brain.NoteInfo
	for _, sb := range s.Sources {
		notes, err := sb.Find(f)
		if err != nil {
			return nil, err
		}
		all = append(all, stamp(notes, sb.Name)...)
	}
	return all, nil
}

func (s *Scope) orphans() ([]brain.NoteInfo, error) {
	var all []brain.NoteInfo
	for _, sb := range s.Sources {
		paths, err := sb.Orphans()
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			all = append(all, brain.NoteInfo{Brain: sb.Name, Path: p})
		}
	}
	return all, nil
}

func stamp(notes []brain.NoteInfo, name string) []brain.NoteInfo {
	for i := range notes {
		notes[i].Brain = name
	}
	return notes
}

func printScopedNotes(notes []brain.NoteInfo, withBrain bool) {
	for _, n := range notes {
		s := n.Summary
		if s == "" {
			s = "(no summary)"
		}
		if withBrain {
			fmt.Printf("%-14s %-46s | %s\n", n.Brain, n.Path, s)
		} else {
			fmt.Printf("%-52s | %s\n", n.Path, s)
		}
	}
}

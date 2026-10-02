package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A summary hand-edited to contain ": " makes the whole block invalid YAML.
const brokenNote = "---\ntype: reference\ntags: [domain]\nsummary: Analysis for the parser: implemented\nretrieved: 2026-09-24\n---\n\n# Title\n\nbody\n"

func TestBrokenFrontMatterIsReportedNotMissing(t *testing.T) {
	b := newBrain(t)
	writeFile(t, b.Root, "domain/broken.md", brokenNote)

	rep, err := b.Lint(LintOptions{Summary: true})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, f := range rep.Findings {
		if f.Path == "domain/broken.md" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "not valid YAML") {
		t.Fatalf("lint finding = %q, want a YAML error", msg)
	}
}

func TestAppendRefusesBrokenFrontMatter(t *testing.T) {
	b := newBrain(t)
	writeFile(t, b.Root, "domain/broken.md", brokenNote)

	if err := b.Append("domain/broken.md", "more"); err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("Append error = %v, want a YAML error", err)
	}
}

func TestUpdateFieldsQuotesAndRepairs(t *testing.T) {
	b := newBrain(t)
	writeFile(t, b.Root, "domain/broken.md", brokenNote)

	values := map[string]string{
		"summary":   `Analysis for the parser: implemented "v2" in #42`,
		"freshness": "2026-10-02: measured on target",
	}
	if err := b.UpdateFields("domain/broken.md", values); err != nil {
		t.Fatal(err)
	}
	n, err := b.Load("domain/broken.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.FMErr != nil {
		t.Fatalf("front matter still invalid: %v", n.FMErr)
	}
	if n.FM.Summary != values["summary"] || n.FM.Freshness != values["freshness"] {
		t.Fatalf("got summary %q, freshness %q", n.FM.Summary, n.FM.Freshness)
	}
	if n.FM.Retrieved != "2026-09-24" || n.FM.Type != "reference" || len(n.FM.Tags) != 1 {
		t.Fatalf("other fields changed: %+v", n.FM)
	}
	if !strings.HasSuffix(n.RawFM, "freshness: \"2026-10-02: measured on target\"") {
		t.Fatalf("absent key not added at the end of the block:\n%s", n.RawFM)
	}
	if n.Body != "\n# Title\n\nbody\n" {
		t.Fatalf("body changed: %q", n.Body)
	}
	if err := b.Append("domain/broken.md", "more"); err != nil {
		t.Fatalf("Append after repair: %v", err)
	}
}

func TestUpdateFieldsReplacesContinuationLines(t *testing.T) {
	b := newBrain(t)
	writeFile(t, b.Root, "domain/folded.md", "---\nsummary: >\n  old\n  text\ntype: reference\n---\nbody\n")

	if err := b.UpdateFields("domain/folded.md", map[string]string{"summary": "new"}); err != nil {
		t.Fatal(err)
	}
	n, err := b.Load("domain/folded.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.RawFM != "summary: new\ntype: reference" {
		t.Fatalf("block = %q", n.RawFM)
	}
}

func TestUpdateFieldsRejectsMultiLineValue(t *testing.T) {
	b := newBrain(t)
	writeFile(t, b.Root, "domain/note.md", "---\nsummary: s\n---\nbody\n")

	if err := b.UpdateFields("domain/note.md", map[string]string{"summary": "a\nb"}); err == nil {
		t.Fatal("expected an error for a multi-line summary")
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

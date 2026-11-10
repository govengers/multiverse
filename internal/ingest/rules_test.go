package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFilingRulesAt(t *testing.T) {
	dir := t.TempDir()
	if got, err := filingRulesAt(dir); err != nil || got != "" {
		t.Fatalf("no brain.yaml: got %q, %v; want empty, nil", got, err)
	}

	writeFile(t, filepath.Join(dir, ".multi", "brain.yaml"), "name: b\nfiling_rules: filing-rules\n")
	if _, err := filingRulesAt(dir); err == nil {
		t.Fatal("missing filing_rules note: want an error, got nil")
	}

	writeFile(t, filepath.Join(dir, "filing-rules.md"),
		"---\ntype: meta\nsummary: rules\n---\n# Filing rules\n\n- Content notes go in `domain/` or `operations/`.\n")
	got, err := filingRulesAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := "- Content notes go in `domain/` or `operations/`."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPromptsUseFilingRules(t *testing.T) {
	custom := "- CUSTOM RULE with `backticks` and 100% literal @@."
	job := BuildJobPrompt(Job{}, &Ledger{}, "delta", "/r.md", custom)
	groom := buildGroomPrompt("lint", "orphans", "oversized", "/r.md", custom)
	for name, p := range map[string]string{"job": job, "groom": groom} {
		if !strings.Contains(p, custom) {
			t.Errorf("%s prompt lacks the custom rules verbatim", name)
		}
		if strings.Contains(p, "wilde-it/") || strings.Contains(p, "dd.MM.yyyy in bodies") {
			t.Errorf("%s prompt still carries the built-in layout", name)
		}
		if strings.Contains(p, "%!") {
			t.Errorf("%s prompt has a formatting error", name)
		}
	}

	job = BuildJobPrompt(Job{}, &Ledger{}, "delta", "/r.md", "")
	groom = buildGroomPrompt("lint", "orphans", "oversized", "/r.md", "")
	if !strings.Contains(job, "**Directory = domain, type = front matter.**") || strings.Contains(job, "@@") {
		t.Error("job prompt without filing rules lacks the built-in layout")
	}
	if !strings.Contains(groom, "Directory = domain, type = front matter, one fact per note") || strings.Contains(groom, "@@") {
		t.Error("groom prompt without filing rules lacks the built-in layout")
	}
}

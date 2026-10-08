package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestExploreTextFitsBudgetInValueOrder(t *testing.T) {
	line := func(i int) string { return fmt.Sprintf("- n%03d.md | %s\n", i, strings.Repeat("s", 280)) }
	var seeds, linked []string
	for i := 0; i < 30; i++ {
		seeds = append(seeds, line(i))
	}
	for i := 30; i < 120; i++ {
		linked = append(linked, line(i))
	}
	body := strings.Repeat("body line\n", 600) // 6 KB
	bodies := []exploreBody{{"a.md", body}, {"b.md", body}, {"c.md", body}}

	out := exploreText("# explore: q\n", seeds, linked, bodies, 24000)

	if len(out) > 24000 {
		t.Fatalf("output %d bytes exceeds budget", len(out))
	}
	if !strings.Contains(out, "n029.md") {
		t.Error("every seed should fit before bodies and linked")
	}
	if !strings.Contains(out, "## body: c.md") {
		t.Error("bodies should take budget before linked summaries")
	}
	if strings.Contains(out, "n119.md") {
		t.Error("the linked tail should be cut")
	}
	if !strings.Contains(out, "linked") || !strings.Contains(out, "# cut to stay under --max-bytes 24000") {
		t.Errorf("trailer should count the cut; got tail %q", out[len(out)-200:])
	}
}

func TestExploreTextTruncatesBodyAtLineBoundary(t *testing.T) {
	body := strings.Repeat("body line\n", 3000) // 30 KB
	out := exploreText("# h\n", []string{"- a.md | s\n"}, nil, []exploreBody{{"a.md", body}}, 24000)

	if len(out) > 24000 {
		t.Fatalf("output %d bytes exceeds budget", len(out))
	}
	if !strings.Contains(out, "body line\n… (truncated: multi read a.md)\n") {
		t.Error("a partly fitting body should end on a whole line plus the truncation marker")
	}
}

func TestExploreTextUncapped(t *testing.T) {
	body := strings.Repeat("x\n", 50000)
	out := exploreText("# h\n", []string{"- a.md | s\n"}, nil, []exploreBody{{"a.md", body}}, 0)
	if !strings.Contains(out, body) || strings.Contains(out, "# cut") {
		t.Error("max-bytes 0 should print everything")
	}
}

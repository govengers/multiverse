package ingest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTranscriptDelta(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "transcript.jsonl")
	lines := `{"type":"user","uuid":"u1","message":{"role":"user","content":"hello there"}}
{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hi back"},{"type":"tool_use","name":"Bash"}]}}
{"type":"user","uuid":"u2","message":{"role":"user","content":"second question"}}
`
	if err := os.WriteFile(tp, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	digest, cursor, last, hasHuman, err := TranscriptDelta(tp, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasHuman {
		t.Error("expected hasHuman true")
	}
	if cursor != 3 {
		t.Errorf("cursor = %d, want 3", cursor)
	}
	if last != "u2" {
		t.Errorf("lastUUID = %q, want u2", last)
	}
	for _, want := range []string{"hello there", "hi back", "second question", "[tool: Bash]"} {
		if !contains(digest, want) {
			t.Errorf("digest missing %q\n%s", want, digest)
		}
	}

	// From the end: no new content.
	d2, c2, _, _, err := TranscriptDelta(tp, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if trim(d2) != "" {
		t.Errorf("expected empty delta past cursor, got %q", d2)
	}
	if c2 != 3 {
		t.Errorf("cursor unchanged = %d, want 3", c2)
	}
}

func TestParseTouched(t *testing.T) {
	report := `some preamble
<!-- INGEST-TOUCHED
Lessons/Foo.md | created | captured the foo gotcha
Atlas/Bar-MOC.md | updated | linked Foo
bad-line-without-pipe
-->
human summary here
INGEST-STATUS: done`
	got := parseTouched(report, 2)
	if len(got) != 2 {
		t.Fatalf("got %d notes, want 2: %+v", len(got), got)
	}
	if got[0].Path != "Lessons/Foo.md" || got[0].Action != "created" || got[0].Summary != "captured the foo gotcha" {
		t.Errorf("note[0] = %+v", got[0])
	}
	if got[1].Path != "Atlas/Bar-MOC.md" || got[1].Run != 2 {
		t.Errorf("note[1] = %+v", got[1])
	}
}

func TestParseTouchedNoBlock(t *testing.T) {
	if got := parseTouched("no markers here\nINGEST-STATUS: done", 1); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestLedgerAddTouchedDedup(t *testing.T) {
	l := &Ledger{}
	l.AddTouched([]TouchedNote{{Path: "A.md", Action: "created", Summary: "v1"}})
	l.AddTouched([]TouchedNote{{Path: "A.md", Action: "updated", Summary: "v2"}, {Path: "B.md", Action: "created"}})
	if len(l.TouchedNotes) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(l.TouchedNotes), l.TouchedNotes)
	}
	if l.TouchedNotes[0].Action != "updated" || l.TouchedNotes[0].Summary != "v2" {
		t.Errorf("A.md not updated in place: %+v", l.TouchedNotes[0])
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	t.Setenv("INGESTER_HOME", t.TempDir())
	l, err := LoadLedger("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	l.Cursor = 7
	l.AddTouched([]TouchedNote{{Path: "X.md", Action: "created"}})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLedger("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cursor != 7 || len(got.TouchedNotes) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"abc-123_x.y": "abc-123_x.y",
		"a/b c:d":     "a-b-c-d",
		"--weird--":   "weird",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHerdrEnvStripped(t *testing.T) {
	got := withoutHerdr([]string{"HERDR_ENV=1", "PATH=/bin", "HERDR_PANE_ID=w1:p3", "MY_HERDR=keep"})
	if len(got) != 2 || got[0] != "PATH=/bin" || got[1] != "MY_HERDR=keep" {
		t.Errorf("withoutHerdr = %q", got)
	}
	// The launch prefix clears what an already-running tmux server hands down,
	// and must not fail when there is nothing to clear.
	for _, env := range [][]string{
		{"PATH=/usr/bin:/bin", "HERDR_ENV=1", "HERDR_PANE_ID=w1:p3", "KEEP=1"},
		{"PATH=/usr/bin:/bin", "KEEP=1"},
	} {
		cmd := exec.Command("sh", "-c", unsetHerdr+"env")
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh: %v", err)
		}
		if contains(string(out), "HERDR_") || !contains(string(out), "KEEP=1") {
			t.Errorf("env after unsetHerdr:\n%s", out)
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' {
			s = s[:len(s)-1]
		} else {
			break
		}
	}
	return s
}

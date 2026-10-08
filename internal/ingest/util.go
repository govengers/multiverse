// Package ingest steers an interactive Claude Code session (on the user's
// subscription, via tmux) to integrate the learnings of a finished Claude session
// into a target brain — weaving them into the existing note graph rather
// than appending standalone files. It is the orchestration layer: ledger, queue,
// tmux lifecycle, steering, completion detection. The actual graph-aware editing is
// done by the steered agent through the `multi` CLI.
package ingest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitize makes a string safe to embed in a filename.
func sanitize(s string) string {
	s = unsafeName.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// nowStamp is an RFC3339 UTC timestamp for ledger/job metadata.
func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

// fileStamp is a filesystem-safe UTC timestamp for filenames (sortable).
func fileStamp() string { return time.Now().UTC().Format("2006-01-02T15-04-05Z") }

// isFileExec reports whether p is a regular, executable file.
func isFileExec(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// notify raises a macOS notification; a no-op where osascript is missing.
func notify(msg string) {
	_ = exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", msg, "ingester")).Run()
}

// logf appends a line to the dispatcher log and echoes it to stderr.
func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s\n", nowStamp(), msg)
	if f, err := os.OpenFile(filepath.Join(Home(), "dispatch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
	fmt.Fprint(os.Stderr, line)
}

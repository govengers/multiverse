package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds how long we wait for the steered agent to finish one job.
const DefaultTimeout = 15 * time.Minute

// Dispatch drains the queue under an exclusive lock. For each session it runs the
// newest pending job (older ones are superseded). Safe to invoke concurrently — only
// the lock holder proceeds; everyone else exits immediately.
func Dispatch(timeout time.Duration, verbose bool) error {
	lock, held, err := acquireLock()
	if err != nil {
		return err
	}
	if !held {
		if verbose {
			fmt.Println("another dispatcher holds the lock; exiting")
		}
		return nil
	}
	defer releaseLock(lock)

	for {
		jobs, err := PendingJobs()
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}

		// Coalesce by session: keep the newest job per session (jobs are oldest-first,
		// so the last seen per session is newest); supersede the rest.
		newest := map[string]Job{}
		var order []string
		for _, j := range jobs {
			if _, seen := newest[j.SessionID]; !seen {
				order = append(order, j.SessionID)
			}
			newest[j.SessionID] = j
		}
		for _, j := range jobs {
			if newest[j.SessionID].file != j.file {
				j.complete(true) // superseded by a newer job for the same session
			}
		}

		for _, sid := range order {
			j := newest[sid]
			ok := processJob(j, timeout, verbose)
			j.complete(ok)
		}
	}
}

// processJob runs one integration cycle for a session: compute the transcript delta
// since the ledger cursor, steer the agent over it, wait for the report, then update
// the ledger and sync. Returns false on a hard failure (job goes to dead-letter).
func processJob(j Job, timeout time.Duration, verbose bool) bool {
	if err := ensureDirs(); err != nil {
		logf("ensure dirs: %v", err)
		return false
	}
	ledger, err := LoadLedger(j.SessionID)
	if err != nil {
		logf("ledger load (%s): %v", j.SessionID, err)
		return false
	}

	digest, newCursor, lastUUID, hasHuman, err := TranscriptDelta(j.TranscriptPath, ledger.Cursor)
	if err != nil {
		logf("transcript (%s): %v", j.TranscriptPath, err)
		return false
	}
	if !hasHuman {
		if verbose {
			fmt.Println("no human messages; skipping")
		}
		return true
	}
	if strings.TrimSpace(digest) == "" {
		// Nothing new since the last run — just advance the cursor.
		ledger.Cursor = newCursor
		ledger.LastUUID = lastUUID
		_ = ledger.Save()
		if verbose {
			fmt.Println("no new content since last run; cursor advanced")
		}
		return true
	}

	rules, err := FilingRules()
	if err != nil {
		logf("filing rules: %v", err)
		return false
	}

	ledger.Runs++
	stamp := fileStamp() + "_" + sanitize(j.SessionID)
	reportPath := filepath.Join(reportsDir(), stamp+".md")
	promptPath := filepath.Join(promptsDir(), stamp+".md")
	if err := os.WriteFile(promptPath, []byte(BuildJobPrompt(j, ledger, digest, reportPath, rules)), 0o644); err != nil {
		logf("write prompt: %v", err)
		return false
	}

	if err := EnsureSession(); err != nil {
		logf("tmux ensure: %v", err)
		return false
	}
	if err := SteerJob(promptPath); err != nil {
		logf("steer: %v", err)
		return false
	}

	report, ok := waitForReport(reportPath, timeout)
	if !ok {
		if PaneLoggedOut() {
			logf("claude in %s is logged out; killed the session so the next job starts a fresh one: %s", SessionName, reportPath)
			_ = KillSession()
			notify("DeepThought ingester: claude was logged out, session restarted. If this repeats, run claude /login.")
			return false
		}
		logf("timeout waiting for report: %s", reportPath)
		return false
	}

	ledger.AddTouched(parseTouched(report, ledger.Runs))
	ledger.Cursor = newCursor
	ledger.LastUUID = lastUUID
	if err := ledger.Save(); err != nil {
		logf("ledger save: %v", err)
	}
	_ = SyncBrain()    // backstop push in case the agent forgot
	_ = ReindexBrain() // keep `multi similar` current with what was just written
	if verbose {
		fmt.Printf("integrated run %d for %s\n", ledger.Runs, j.SessionID)
	}
	return true
}

// waitForReport polls for the sentinel report file containing the status trailer.
func waitForReport(path string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "INGEST-STATUS:") {
			return string(data), true
		}
		if PaneLoggedOut() {
			return "", false
		}
		time.Sleep(5 * time.Second)
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data), strings.Contains(string(data), "INGEST-STATUS:")
	}
	return "", false
}

// parseTouched extracts the machine-readable touched-notes block from a report.
func parseTouched(report string, run int) []TouchedNote {
	const open, close = "<!-- INGEST-TOUCHED", "-->"
	i := strings.Index(report, open)
	if i < 0 {
		return nil
	}
	rest := report[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return nil
	}
	var out []TouchedNote
	for _, line := range strings.Split(rest[:j], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		n := TouchedNote{Path: strings.TrimSpace(parts[0]), Action: strings.TrimSpace(parts[1]), Run: run}
		if len(parts) >= 3 {
			n.Summary = strings.TrimSpace(parts[2])
		}
		if n.Path != "" {
			out = append(out, n)
		}
	}
	return out
}

// acquireLock takes a non-blocking exclusive flock. Returns (file, true) if held, or
// (nil, false) when another process already holds it.
func acquireLock() (*os.File, bool, error) {
	if err := ensureDirs(); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, false, nil
	}
	return f, true, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

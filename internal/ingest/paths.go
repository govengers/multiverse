package ingest

import (
	"os"
	"path/filepath"
)

// Home is the ingester's data root. Defaults to ~/.claude/ingester, overridable via
// INGESTER_HOME (for tests and isolated environments).
func Home() string {
	if d := os.Getenv("INGESTER_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ingester"
	}
	return filepath.Join(home, ".claude", "ingester")
}

func stateDir() string       { return filepath.Join(Home(), "state") }           // per-session ledgers
func queueDir() string       { return filepath.Join(Home(), "queue") }           // pending jobs
func doneDir() string        { return filepath.Join(Home(), "queue", "done") }   // processed jobs
func failedDir() string      { return filepath.Join(Home(), "queue", "failed") } // dead-letter
func reportsDir() string     { return filepath.Join(Home(), "reports") }         // sentinel reports
func promptsDir() string     { return filepath.Join(Home(), "prompts") }         // per-job prompt files
func runnerSettings() string { return filepath.Join(Home(), "runner-settings.json") }

// lockPath is GLOBAL — deliberately NOT under Home(). It guards the single shared tmux
// session (which has a fixed name), so every dispatcher and foreground run serialises
// on it regardless of INGESTER_HOME. A per-Home lock would let two clients (e.g. a test
// and the live hook) steer the same session at once and interleave their keystrokes.
func lockPath() string { return filepath.Join(os.TempDir(), SessionName+".lock") }

// ensureDirs creates all working directories.
func ensureDirs() error {
	for _, d := range []string{stateDir(), queueDir(), doneDir(), failedDir(), reportsDir(), promptsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// writeRunnerSettings writes the settings file for the steered Claude session. Its
// empty hooks map adds no hooks, but Claude merges it with the user settings, so the
// global hooks still fire; the MULTI_INGEST env guard is what stops recursion.
func writeRunnerSettings() error {
	if err := ensureDirs(); err != nil {
		return err
	}
	return os.WriteFile(runnerSettings(), []byte(`{"hooks":{}}`+"\n"), 0o644)
}

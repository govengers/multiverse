package ingest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SessionName is the dedicated tmux session that hosts the steered Claude agent.
const SessionName = "multi-ingest"

// unsetHerdr is the shell prefix that drops every HERDR_* variable before claude
// starts. An already-running tmux server hands its global env (possibly started
// from a herdr pane) to new sessions, which cmd.Env cannot reach.
const unsetHerdr = `unset $(env | grep -o '^HERDR_[A-Za-z0-9_]*'); `

// EnsureSession makes sure the dedicated tmux session is alive with an interactive
// Claude (Sonnet, subscription quota) running in the target brain's dir. It is
// recursion-guarded (MULTI_INGEST=1) so its own Stop event cannot re-trigger
// ingestion; the runner settings add no hooks but do not disable the global ones.
// It never sees HERDR_* (the hook runs in a herdr pane), otherwise herdr's
// SessionStart hook registers it as that pane's agent. A dead session is recreated.
func EnsureSession() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}
	if SessionAlive() {
		return nil
	}
	if err := writeRunnerSettings(); err != nil {
		return err
	}
	brainDir, err := BrainDir()
	if err != nil {
		return err
	}
	launch := unsetHerdr + fmt.Sprintf("exec %q --model sonnet --dangerously-skip-permissions --settings %q",
		claudePath(), runnerSettings())
	cmd := exec.Command("tmux", "new-session", "-d",
		"-s", SessionName,
		"-x", "220", "-y", "50",
		"-c", brainDir,
		"-e", "MULTI_INGEST=1",
		"sh", "-c", launch)
	cmd.Env = withoutHerdr(os.Environ()) // a tmux server started here must not inherit them
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %v: %s", err, out)
	}
	time.Sleep(6 * time.Second) // let the TUI boot before we steer it
	return nil
}

// withoutHerdr returns env minus every HERDR_* entry.
func withoutHerdr(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "HERDR_") {
			out = append(out, kv)
		}
	}
	return out
}

// SessionAlive reports whether the ingestion session exists.
func SessionAlive() bool {
	return exec.Command("tmux", "has-session", "-t", SessionName).Run() == nil
}

// PaneLoggedOut reports whether the steered Claude answered with its logged-out
// banner. A long-lived TUI can lose its OAuth session while a freshly started
// claude still reads valid credentials, so the cure is a restart.
func PaneLoggedOut() bool {
	out, err := exec.Command("tmux", "capture-pane", "-t", SessionName, "-p").Output()
	return err == nil && strings.Contains(string(out), "Please run /login")
}

// KillSession terminates the ingestion session.
func KillSession() error {
	return exec.Command("tmux", "kill-session", "-t", SessionName).Run()
}

// SteerJob clears the session's context and points it at the job's prompt file.
func SteerJob(promptPath string) error {
	if err := submitPrompt("/clear"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	instr := fmt.Sprintf("Execute the brain ingestion job described in @%s — follow it exactly, and finish by writing the report file it specifies.", promptPath)
	return submitPrompt(instr)
}

// submitPrompt types text into the input and reliably submits it. The Claude TUI
// often drops an Enter that arrives right after the text lands, so we resend Enter
// until the input box no longer shows our text (verified by reading the pane).
func submitPrompt(text string) error {
	if err := sendLiteral(text); err != nil {
		return err
	}
	marker := submitMarker(text)
	for attempt := 0; attempt < 6; attempt++ {
		time.Sleep(1200 * time.Millisecond)
		if err := sendEnter(); err != nil {
			return err
		}
		time.Sleep(800 * time.Millisecond)
		if !inputShows(marker) {
			return nil // input box cleared → submitted
		}
	}
	return fmt.Errorf("could not submit prompt to %s (input never cleared)", SessionName)
}

// submitMarker is a short, stable prefix of typed text, used to detect whether it is
// still sitting unsent in the input box.
func submitMarker(text string) string {
	t := strings.TrimSpace(text)
	if len(t) > 24 {
		t = t[:24]
	}
	return t
}

// inputShows reports whether the session's input line (the last ❯ prompt) still
// contains the marker — i.e. the text has not been submitted yet.
func inputShows(marker string) bool {
	out, err := exec.Command("tmux", "capture-pane", "-t", SessionName, "-p").Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "❯") {
			return strings.Contains(lines[i], marker)
		}
	}
	return strings.Contains(string(out), marker)
}

func sendLiteral(text string) error {
	return exec.Command("tmux", "send-keys", "-t", SessionName, "-l", text).Run()
}

func sendEnter() error {
	return exec.Command("tmux", "send-keys", "-t", SessionName, "Enter").Run()
}

// claudePath locates the claude binary.
func claudePath() string {
	home, _ := os.UserHomeDir()
	if c := filepath.Join(home, ".local", "bin", "claude"); isFileExec(c) {
		return c
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return "claude"
}

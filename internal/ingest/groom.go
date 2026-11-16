package ingest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GroomTimeout bounds one grooming run. Grooming touches many notes, so it
// gets more room than a single ingestion job.
const GroomTimeout = 45 * time.Minute

// Groom steers the agent through one vault-maintenance cycle: fix lint
// findings, split notes that grew past the canonical size, merge fresh
// near-duplicates, wire recent notes laterally, keep MOCs curated. The
// deterministic weekly health check only OBSERVES decay - this is the
// counterpart that repairs it. Meant to run from cron.
func Groom(timeout time.Duration, verbose bool) error {
	lock, held, err := acquireLock()
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("the ingestion session is busy (another dispatcher/run holds the lock); try again later")
	}
	defer releaseLock(lock)
	if err := ensureDirs(); err != nil {
		return err
	}
	// lint exits non-zero on findings and orphans can be long - both outputs
	// are input for the agent, not errors for us.
	rules, err := FilingRules()
	if err != nil {
		return err
	}
	lint := multiOut("lint")
	orphans := clipLines(multiOut("orphans"), 120)
	oversized := clipLines(oversizedNotes(20*1024), 60)
	stamp := fileStamp() + "_groom"
	reportPath := filepath.Join(reportsDir(), stamp+".md")
	promptPath := filepath.Join(promptsDir(), stamp+".md")
	if err := os.WriteFile(promptPath, []byte(buildGroomPrompt(lint, orphans, oversized, reportPath, rules)), 0o644); err != nil {
		return err
	}
	if err := EnsureSession(); err != nil {
		return err
	}
	if err := SteerJob(promptPath); err != nil {
		return err
	}
	if _, ok := waitForReport(reportPath, timeout); !ok {
		return fmt.Errorf("timeout waiting for groom report: %s", reportPath)
	}
	_ = SyncBrain()
	_ = ReindexBrain()
	if verbose {
		fmt.Println("grooming run complete")
	}
	return nil
}

func multiOut(args ...string) string {
	out, _ := exec.Command(multiPath(), append([]string{"--brain", BrainName}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

// oversizedNotes lists content notes above limit bytes, largest first, as
// "<bytes> <path>" lines. Accretion notes are the main retrieval hazard: one
// blurry embedding and a body that swamps `explore` output.
// ponytail: shells out to find; a `multi lint --size` rule would be the home for this.
func oversizedNotes(limit int) string {
	root := brainRoot()
	if root == "" {
		return ""
	}
	out, _ := exec.Command("sh", "-c",
		fmt.Sprintf(`cd %q && find . -name '*.md' -not -path './.git/*' -not -path './atlas/*' -not -path './journals/*' -size +%dc -exec wc -c {} + | sort -rn | grep -v ' total$'`, root, limit)).Output()
	return strings.TrimSpace(string(out))
}

func brainRoot() string {
	out, err := exec.Command(multiPath(), "brain", "list").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(strings.TrimLeft(line, "*> "))
		if len(f) >= 2 && f[0] == BrainName {
			return f[len(f)-1]
		}
	}
	return ""
}

func clipLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… (%d more lines - run the command yourself for the rest)", len(lines)-n)
}

// buildGroomPrompt assembles the maintenance instructions with the current
// lint, orphan and size state baked in, so the agent starts from facts, not scans.
// rules replaces the built-in layout summary when the brain names its own
// filing rules (see FilingRules).
func buildGroomPrompt(lint, orphans, oversized, reportPath, rules string) string {
	if rules == "" {
		rules = strings.ReplaceAll(defaultGroomRules, "@@", "`")
	}
	b := BrainName
	return fmt.Sprintf(strings.ReplaceAll(`# Brain grooming job

You are running the weekly maintenance cycle for the %[1]s brain. The automated
health check only observes decay - your job is to repair it. Work the phases in
order and stay conservative: everything is git-tracked, but aim for net-positive
edits only. Zero information loss: nothing is deleted, only merged, split, moved
or marked deprecated.

## Brain safety (non-negotiable)
- Every brain command MUST be pinned: @@multi --brain %[1]s …@@ (the @@--brain@@ flag goes
  BEFORE the subcommand). A bare @@multi@@ targets the wrong (active) brain.
- NEVER bypass @@multi@@ for git (no direct git, no other note app).
- Create every new note with @@multi --brain %[1]s write --dir <domain>@@ (front matter is
  generated; pass --summary, --source, --freshness). Direct file edits: BODY only,
  plus @@status@@/@@updated@@ in front matter when deprecating.

## Layout rules you are maintaining
%[6]s

## Phase 1 - lint findings: fix every one
@@@@@@
%[2]s
@@@@@@
Missing freshness/source: derive honest values from the note's created date and
content; never fabricate. Other rule violations: repair per the rule's intent.

## Phase 2 - oversized notes: split back to canonical
@@@@@@
%[3]s
@@@@@@
For each note over 20 KB: read it fully; rewrite it in place as the current state
of its fact (target under 12 KB); move every dated/session section VERBATIM into
journals/<basename>-log.md (create with @@multi write --dir journals --type journal@@,
then @@append@@); extract distinct durable facts into their own notes in the same
directory (one fact each, linked both ways). Every sentence of the original ends up
in exactly one of: the note, a fact note, the log.

## Phase 3 - near-duplicates among recent notes
For roughly the 30 newest notes (@@multi --brain %[1]s list --json@@, sort by created):
run @@multi --brain %[1]s similar --note <name>@@. Neighbours above ~0.85 cosine that
restate the same fact: merge into the better-named note (zero-loss - fold in anything
new, preserve contradictions under an "## Open question" heading naming both
sources), delete the loser file, retarget its wikilinks brain-wide, collapse duplicate
MOC lines. Neighbours that are distinct facts on the same topic: cross-link both.

## Phase 4 - orphans: wire them laterally
@@@@@@
%[4]s
@@@@@@
Skip journals/ entries. For each remaining orphan: @@multi --brain %[1]s similar --note
<name>@@, then add it to the @@## Related@@ section of its two closest notes and add
those two to its own Related section. Add a MOC line only when the orphan is a
project-context, architecture or decision note. An orphan that is superseded or
worthless: @@status: deprecated@@ with a banner instead of linking.

## Phase 5 - dead links and MOC freshness
For every note you touched: check each @@[[link]]@@ resolves (@@multi --brain %[1]s summary
<name>@@ errors when it does not); retarget or unwrap dead ones. For every MOC you
touched: entries must exist, section lists must reflect reality, no session sections
(move any to journals/<moc>-log.md).

## When done - write the report (this is how completion is detected)
Write your report to this exact path, OVERWRITING it:

    %[5]s

The report MUST end with the status trailer:

    (short human summary: findings fixed, notes split, dupes merged, orphans wired, MOCs updated)

    INGEST-STATUS: done

Use @@multi --brain %[1]s sync@@ as your final step to push. Write the report LAST.
`, "@@", "`"), b, lint, oversized, orphans, reportPath, strings.TrimSpace(rules))
}

// defaultGroomRules summarise the built-in filing rules (see defaultJobRules)
// for a brain whose brain.yaml names no filing_rules note. @@ stands for a
// backtick.
const defaultGroomRules = `Directory = domain, type = front matter, one fact per note, unique kebab-case
basenames, bare-basename wikilinks, canonical notes under ~12 KB with chronology in
journals/<basename>-log.md, tags from the existing vocabulary only, superseded notes
@@status: deprecated@@ with a banner (never deleted), atlas/<domain>.md MOCs curated
(load-bearing notes only, no session sections).
`

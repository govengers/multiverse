package ingest

import (
	"fmt"
	"strings"
)

// BuildJobPrompt assembles the markdown instruction file the steered agent reads. It
// carries four things: the brain-safety rules, the filing rules of the domain layout
// (directory = domain, type = front matter, one fact per note, chronology in
// journals/), the per-session continuity ledger (notes already touched this session,
// so the agent extends rather than duplicates), and the new conversation delta to
// integrate. The agent signals completion by writing the report file with an
// INGEST-STATUS trailer. rules replaces the built-in filing rules when the brain
// names its own (see FilingRules).
func BuildJobPrompt(j Job, l *Ledger, digest, reportPath, rules string) string {
	if rules == "" {
		rules = strings.ReplaceAll(defaultJobRules, "@@", "`")
	}
	var prior strings.Builder
	if len(l.TouchedNotes) == 0 {
		prior.WriteString("_(none yet - this is the first integration run for this session)_\n")
	} else {
		for _, n := range l.TouchedNotes {
			fmt.Fprintf(&prior, "- `%s` - %s (%s)\n", n.Path, n.Summary, n.Action)
		}
	}
	b := BrainName
	return fmt.Sprintf(strings.ReplaceAll(`# Brain ingestion job

You are integrating the learnings of a just-finished Claude Code session into the
%[1]s brain. This is NOT "evaluate, then drop a new file somewhere." Your job is to
weave durable knowledge into the EXISTING note graph: extend the note that already
holds the fact, wire lateral links, create a new note only when the fact is new.

## Brain safety (non-negotiable)
- Every brain command MUST be pinned: @@multi --brain %[1]s …@@ (the @@--brain@@ flag goes
  BEFORE the subcommand). A bare @@multi@@ targets the wrong (active) brain.
- NEVER bypass @@multi@@ for git (no direct git, no other note app) - @@multi@@ owns commits.
- Create EVERY new note with @@multi --brain %[1]s write --dir <domain> …@@ - it generates the
  canonical front matter and refuses without a summary. NEVER hand-write a new-note file:
  the brain uses TOP-LEVEL type / status / tags / summary / source, NOT Claude's memory
  schema (name / description / metadata:). Always pass --source and --freshness.
- Extend an existing note with @@multi --brain %[1]s append@@, or edit its file directly
  to rewrite a section of the BODY (never the front matter, except @@status@@/@@updated@@
  when deprecating), then let multi commit.

## How this brain is laid out (filing rules)
%[5]s

## Already integrated THIS session - continue, don't duplicate
%[2]s
When the new content below belongs in one of these notes, EXTEND that note instead of
making a new one.

## Integration procedure
1. Read the conversation delta at the end of this file.
2. Identify durable learnings (decisions, patterns, gotchas, research outcomes,
   corrections, deployment knowledge, project context). Skip routine chatter.
3. For each learning, explore its neighbourhood in ONE call:
   @@multi --brain %[1]s explore "<q1>" "<q2>" "<q3>" --bodies 3@@
   (3-5 short queries: the technology, the project, the verb). Read the summaries;
   @@multi --brain %[1]s read <note>@@ anything else that looks relevant. Note the
   directory the neighbours live in: that is the domain to file under.
4. Integrate, in this order of preference:
   a. The fact already exists → extend or rewrite that note's section in place
      (@@append@@ for a genuinely additional detail; direct body edit for a correction).
   b. It is chronology of an existing note → @@journals/<basename>-log.md@@.
   c. It is a new fact → @@multi --brain %[1]s write --dir <domain> --type <type> --title
      "<fact>" --summary "<one specific line>" --tags <t> --source "<where learned>"
      --freshness "<honest line>" --body "<fact>\n\n## Related\n- [[a]]\n- [[b]]"@@
      then add the backlink in at least one of the related notes.
   d. It supersedes an existing note → deprecate the old one (see filing rules).
5. Be conservative with rewrites - improve, don't vandalise. Everything is git-tracked,
   but aim for net-positive edits only.

## Session handoff (always, as the last integration step)
Overwrite the BODY of @@journals/last-session-handoff.md@@ (keep its front matter) with
at most 15 lines: date, project/topic worked on, decisions made, open threads / next
steps, and the notes you touched as @@[[wikilinks]]@@. This note is printed in full by
@@multi wake-up@@, so the next session starts where this one left off - write it for
that reader, and update it even when nothing else was worth integrating.

## When done - write the report (this is how completion is detected)
Write your report to this exact path, OVERWRITING it:

    %[3]s

The report MUST contain a machine-readable touched-notes block and a status trailer:

    <!-- INGEST-TOUCHED
    relative/note/path.md | created|updated|linked|deprecated | one-line what-changed
    relative/other.md | updated | one-line what-changed
    -->

    (a short human summary of what you integrated, or "nothing worth keeping")

    INGEST-STATUS: done

Use @@multi --brain %[1]s sync@@ as your final step to push. Write the report LAST.

---

## Conversation delta to integrate

%[4]s
`, "@@", "`"), b, prior.String(), reportPath, digest, strings.TrimSpace(rules))
}

// defaultJobRules are the filing rules for a brain whose brain.yaml names no
// filing_rules note. @@ stands for a backtick.
const defaultJobRules = `- **Directory = domain, type = front matter.** Top-level directories are domains
  (e.g. hub/, infrastructure/, go/, frontend/, wilde-it/, deepthought/, claude-code/,
  agents/, ...; run @@ls@@ in the brain root for the current list). A note about a
  specific project's instance of a technology goes to the PROJECT dir; reusable
  knowledge about the technology goes to the TECHNOLOGY dir. Never create a new
  top-level directory; never file a content note in atlas/, journals/ or templates/.
- **One fact per note**, kebab-case filename that names the fact, basename unique
  brain-wide. Wikilinks are bare basenames (@@[[note-name]]@@), never paths.
- **Canonical notes stay canonical.** A note describes the CURRENT state of its fact
  and stays under ~12 KB. Never grow it by appending dated "Update dd.MM.yyyy" sections.
  When a fact changed: rewrite the section that changed in place. When something
  happened (a session, a release, a review round): that is chronology and goes to
  @@journals/<note-basename>-log.md@@ (create it via @@multi write --dir journals --type journal@@
  if missing, then @@append@@), linked from the note's Related section. When a genuinely
  new distinct fact emerged: a new note in the same domain dir, linked both ways.
- **Lateral wiring beats MOC wiring.** Every note you create must link at least two
  related notes in its @@## Related@@ section and get a backlink from at least one of
  them. Atlas MOCs (atlas/<domain>.md) are curated maps of load-bearing notes; add a
  MOC line only for a project-context, architecture or decision note that a reader
  would need to find by browsing. Never append session sections to a MOC.
- **Tags are facets from the existing vocabulary** (project, technology, domain).
  Reuse the tags of the neighbour notes @@explore@@ returns; never invent a tag. Keywords
  belong in the summary, not in tags.
- **Superseded knowledge is deprecated, never deleted.** When a new fact replaces an
  old note: set the old note's @@status: deprecated@@, add a first-line banner
  "> **Deprecated dd.MM.yyyy:** <why> - see [[new-note]]", keep the body.
- German date format dd.MM.yyyy in bodies. Never the em-dash character (U+2014).
`

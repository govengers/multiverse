// Recall commands: the session bootstrap (wake-up), the semantic shadow
// index (reindex, similar) and the one-call task retrieval (explore). Search stays the lexical workhorse; these cover
// what substring matching can't — session priming and meaning-based recall.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mxcd/multiverse/internal/brain"
	"github.com/urfave/cli/v3"
)

func wakeupCmd() *cli.Command {
	return &cli.Command{
		Name:    "wake-up",
		Aliases: []string{"wakeup"},
		Usage:   "print the session bootstrap: identity notes in full, pinned facts as one-liners",
		Description: "Emits a small, deterministic context block to load at session start: the\n" +
			"brain's `wakeup:` notes (L0, printed in full — keep them short) followed by\n" +
			"every note with `pinned: true` front matter as `path | summary` lines (L1).",
		Flags: withBrain(&cli.BoolFlag{Name: "json"}),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			type brainWakeup struct {
				Brain    string                `json:"brain"`
				Sections []brain.WakeupSection `json:"sections,omitempty"`
				Facts    []brain.NoteInfo      `json:"facts,omitempty"`
			}
			var all []brainWakeup
			for _, sb := range sc.Sources {
				sections, facts, err := sb.Wakeup()
				if err != nil {
					return fmt.Errorf("%s: %w", sb.Name, err)
				}
				if len(sections) > 0 || len(facts) > 0 {
					all = append(all, brainWakeup{Brain: sb.Name, Sections: sections, Facts: facts})
				}
			}
			if cmd.Bool("json") {
				return printJSON(all)
			}
			if len(all) == 0 {
				fmt.Println("(nothing to wake up with: list notes under `wakeup:` in .multi/brain.yaml or pin notes with `pinned: true`)")
				return nil
			}
			for _, bw := range all {
				fmt.Printf("# wake-up: %s\n", bw.Brain)
				for _, s := range bw.Sections {
					fmt.Printf("\n## %s\n%s", s.Path, s.Body)
					if !strings.HasSuffix(s.Body, "\n") {
						fmt.Println()
					}
				}
				if len(bw.Facts) > 0 {
					fmt.Printf("\n## pinned\n")
					for _, f := range bw.Facts {
						fmt.Printf("- %s | %s\n", f.Path, f.Summary)
					}
				}
			}
			return nil
		},
	}
}

func reindexCmd() *cli.Command {
	return &cli.Command{
		Name:  "reindex",
		Usage: "build/refresh the semantic shadow index (embeddings) for every brain in scope",
		Description: "Embeds each note via the brain's configured OpenAI-compatible endpoint and\n" +
			"caches one vector per note outside the repo. Incremental: unchanged notes are\n" +
			"skipped by content hash. The index is disposable — markdown stays the source\n" +
			"of truth. Brains without an `embeddings:` config are skipped.",
		Flags: withBrain(&cli.BoolFlag{Name: "force", Usage: "discard the index and re-embed everything"}),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			for _, sb := range sc.union() {
				stats, err := sb.Reindex(cmd.Bool("force"), func(line string) {
					fmt.Printf("%-14s %s\n", sb.Name, line)
				})
				if errors.Is(err, brain.ErrNoEmbeddings) {
					fmt.Printf("%-14s skipped (no embeddings config)\n", sb.Name)
					continue
				}
				if err != nil {
					return fmt.Errorf("%s: %w", sb.Name, err)
				}
				fmt.Printf("%-14s embedded %d, kept %d, removed %d\n", sb.Name, stats.Embedded, stats.Kept, stats.Removed)
			}
			return nil
		},
	}
}

func similarCmd() *cli.Command {
	return &cli.Command{
		Name:      "similar",
		Usage:     "semantic search: notes closest in meaning to a query or an existing note",
		ArgsUsage: "<query>",
		Description: "Ranks notes by cosine similarity against the shadow index built by\n" +
			"`multi reindex`. Complements `multi search`: search matches words, similar\n" +
			"matches meaning. With --note, lists the nearest neighbors of that note\n" +
			"instead (dedup and grooming's best friend).",
		Flags: withBrain(
			&cli.StringFlag{Name: "note", Usage: "find neighbors of this note instead of a text query"},
			&cli.IntFlag{Name: "top", Value: 10, Usage: "number of results"},
			&cli.BoolFlag{Name: "json"},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			topK := int(cmd.Int("top"))

			if ref := cmd.String("note"); ref != "" {
				sb, rel, err := sc.resolveNote(ref)
				if err != nil {
					return err
				}
				hits, err := sb.SimilarNote(rel, topK)
				if err != nil {
					return err
				}
				return printSimilar(cmd, stamp(hits, sb.Name), sc.multiSource())
			}

			query := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
			if query == "" {
				return errors.New("usage: multi similar <query> (or --note <note>)")
			}
			var all []brain.NoteInfo
			configured := false
			for _, sb := range sc.Sources {
				hits, err := sb.Similar(query, topK)
				if errors.Is(err, brain.ErrNoEmbeddings) {
					continue
				}
				if err != nil {
					return fmt.Errorf("%s: %w", sb.Name, err)
				}
				configured = true
				all = append(all, stamp(hits, sb.Name)...)
			}
			if !configured {
				return brain.ErrNoEmbeddings
			}
			brain.SortByScore(all)
			if len(all) > topK {
				all = all[:topK]
			}
			return printSimilar(cmd, all, sc.multiSource())
		},
	}
}

func printSimilar(cmd *cli.Command, notes []brain.NoteInfo, withBrain bool) error {
	if cmd.Bool("json") {
		return printJSON(notes)
	}
	for _, n := range notes {
		s := n.Summary
		if s == "" {
			s = "(no summary)"
		}
		if withBrain {
			fmt.Printf("%.3f  %-14s %-46s | %s\n", n.Score, n.Brain, n.Path, s)
		} else {
			fmt.Printf("%.3f  %-52s | %s\n", n.Score, n.Path, s)
		}
	}
	return nil
}

func exploreCmd() *cli.Command {
	return &cli.Command{
		Name:      "explore",
		Usage:     "one-call retrieval for a task: search + similar per query, one hop along links, ranked summaries plus the top bodies",
		ArgsUsage: `"<query>" ["<query>" ...]`,
		Description: "Runs every query through `search` and `similar`, unions the top hits as seeds,\n" +
			"follows their outgoing wikilinks for --hops (MOCs and other hubs are reached but\n" +
			"never expanded), and prints the result ranked: every summary as a `path | summary`\n" +
			"line, then the full body of the --bodies best notes, all within --max-bytes (it fills\n" +
			"seeds, then bodies, then linked, and counts what it cut). Give 3-5 short queries that\n" +
			"name the technologies, projects and the verb of the task; more queries beat\n" +
			"deeper hops. No model in the loop: the caller reads summaries, then bodies.",
		Flags: withBrain(
			&cli.IntFlag{Name: "top", Value: 5, Usage: "hits taken from each of search and similar per query"},
			&cli.IntFlag{Name: "hops", Value: 1, Usage: "link-expansion depth from the seeds (2 already returns a tenth of a big brain)"},
			&cli.IntFlag{Name: "bodies", Value: 3, Usage: "print the full body of this many top-ranked notes (0 = summaries only)"},
			&cli.IntFlag{Name: "max-bytes", Value: 24000, Usage: "cap the text output; fills seeds, then bodies, then linked (0 = no cap)"},
			&cli.BoolFlag{Name: "json"},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			var queries []string
			for _, q := range cmd.Args().Slice() {
				if q = strings.TrimSpace(q); q != "" {
					queries = append(queries, q)
				}
			}
			if len(queries) == 0 {
				return errors.New(`usage: multi explore "<query>" ["<query>" ...]`)
			}
			opt := brain.ExploreOptions{Top: int(cmd.Int("top")), Hops: int(cmd.Int("hops"))}
			byName := map[string]ScopedBrain{}
			var hits []brain.ExploreHit
			for _, sb := range sc.Sources {
				byName[sb.Name] = sb
				got, err := sb.Explore(queries, opt)
				if err != nil {
					return fmt.Errorf("%s: %w", sb.Name, err)
				}
				for i := range got {
					got[i].Brain = sb.Name
				}
				hits = append(hits, got...)
			}
			brain.SortExplore(hits)
			// A MOC body is a link list, not knowledge: it never takes a body slot.
			var withBody []int
			for i := range hits {
				if len(withBody) == int(cmd.Int("bodies")) {
					break
				}
				if hits[i].Type == "moc" {
					continue
				}
				n, err := byName[hits[i].Brain].Load(hits[i].Path)
				if err != nil {
					return err
				}
				hits[i].Body = n.Body
				withBody = append(withBody, i)
			}
			bodies := len(withBody)
			if cmd.Bool("json") {
				return printJSON(hits)
			}
			if len(hits) == 0 {
				fmt.Println("(nothing found: try fewer or different terms per query)")
				return nil
			}
			seeds, linked := 0, 0
			for _, h := range hits {
				if h.Hop == 0 {
					seeds++
				} else {
					linked++
				}
			}
			header := fmt.Sprintf("# explore: %s\n# %d seeds, %d linked (%d hop), bodies of the top %d\n",
				strings.Join(queries, " | "), seeds, linked, opt.Hops, bodies)
			line := func(h brain.ExploreHit) string {
				s := h.Summary
				if s == "" {
					s = "(no summary)"
				}
				if sc.multiSource() {
					return fmt.Sprintf("- %s:%s | %s\n", h.Brain, h.Path, s)
				}
				return fmt.Sprintf("- %s | %s\n", h.Path, s)
			}
			var seedLines, linkedLines []string
			for _, h := range hits {
				if h.Hop == 0 {
					seedLines = append(seedLines, line(h))
				} else {
					linkedLines = append(linkedLines, line(h))
				}
			}
			var bodyList []exploreBody
			for _, i := range withBody {
				bodyList = append(bodyList, exploreBody{Path: hits[i].Path, Text: hits[i].Body})
			}
			fmt.Print(exploreText(header, seedLines, linkedLines, bodyList, int(cmd.Int("max-bytes"))))
			return nil
		},
	}
}

// exploreBody is one note body explore prints after the summaries.
type exploreBody struct{ Path, Text string }

// exploreText renders explore's text output within maxBytes (0 = no cap). The
// budget fills in order of value: seed summaries, then bodies, then linked
// summaries; a body that only partly fits is cut at a line boundary. Agent
// harnesses move oversized tool output into a file (Claude Code: 30 KB) that the
// agent then often never reads, so one explore must fit one tool result.
func exploreText(header string, seeds, linked []string, bodies []exploreBody, maxBytes int) string {
	const seedHead, linkedHead, trailerRoom, minCut = "\n## seeds\n", "\n## linked\n", 200, 1024
	left := maxBytes - len(header) - trailerRoom
	if maxBytes <= 0 {
		left = int(^uint(0) >> 1)
	}
	take := func(lines []string, head string) []string {
		if len(lines) == 0 || len(head) >= left {
			return nil
		}
		left -= len(head)
		var out []string
		for _, l := range lines {
			if len(l) > left {
				break
			}
			out = append(out, l)
			left -= len(l)
		}
		return out
	}

	keptSeeds := take(seeds, seedHead)
	var keptBodies []string
	truncated := 0
	for _, b := range bodies {
		text := b.Text
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		head := fmt.Sprintf("\n## body: %s\n\n", b.Path)
		if len(head)+len(text) <= left {
			keptBodies = append(keptBodies, head+text)
			left -= len(head) + len(text)
			continue
		}
		marker := fmt.Sprintf("… (truncated: multi read %s)\n", b.Path)
		room := left - len(head) - len(marker)
		if room < minCut {
			break
		}
		cut := text[:room]
		if i := strings.LastIndex(cut, "\n"); i > 0 {
			cut = cut[:i+1]
		}
		keptBodies = append(keptBodies, head+cut+marker)
		left -= len(head) + len(cut) + len(marker)
		truncated++
		break
	}
	keptLinked := take(linked, linkedHead)

	var sb strings.Builder
	sb.WriteString(header)
	for _, part := range []struct {
		head  string
		lines []string
	}{{seedHead, keptSeeds}, {linkedHead, keptLinked}} {
		if len(part.lines) > 0 {
			sb.WriteString(part.head)
			sb.WriteString(strings.Join(part.lines, ""))
		}
	}
	sb.WriteString(strings.Join(keptBodies, ""))

	var cut []string
	if n := len(seeds) - len(keptSeeds); n > 0 {
		cut = append(cut, fmt.Sprintf("%d seeds", n))
	}
	if n := len(linked) - len(keptLinked); n > 0 {
		cut = append(cut, fmt.Sprintf("%d linked", n))
	}
	if n := len(bodies) - len(keptBodies); n > 0 {
		cut = append(cut, fmt.Sprintf("%d bodies", n))
	}
	if truncated > 0 {
		cut = append(cut, "1 body truncated")
	}
	if len(cut) > 0 {
		fmt.Fprintf(&sb, "\n# cut to stay under --max-bytes %d: %s (multi read <path>, or raise --max-bytes)\n",
			maxBytes, strings.Join(cut, ", "))
	}
	return sb.String()
}

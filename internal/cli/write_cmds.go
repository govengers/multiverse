package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mxcd/multiverse/internal/brain"
	"github.com/urfave/cli/v3"
)

func writeCmd() *cli.Command {
	return &cli.Command{
		Name:  "write",
		Usage: "create a note in a target brain (summary enforced, dates auto-filled, auto-committed)",
		Description: "Creates a markdown note that obeys the standing rules. Refuses without a\n" +
			"--summary. Body comes from --body or, with --stdin, standard input. The note\n" +
			"lands in the scope's first target brain unless --brain selects another.",
		Flags: withBrain(
			&cli.StringFlag{Name: "title", Usage: "note title (also the filename unless --path)"},
			&cli.StringFlag{Name: "path", Usage: "explicit vault-relative path"},
			&cli.StringFlag{Name: "dir", Usage: "directory for the note when using --title"},
			&cli.StringFlag{Name: "type", Value: "reference", Usage: "moc|reference|decision|hub|meta"},
			&cli.StringFlag{Name: "status", Value: "active", Usage: "active|draft|deprecated"},
			&cli.BoolFlag{Name: "pinned", Usage: "surface this note in `multi wake-up`"},
			&cli.StringFlag{Name: "summary", Usage: "one-line summary (required)"},
			&cli.StringSliceFlag{Name: "tags", Usage: "tags (repeatable or comma-separated)"},
			&cli.StringFlag{Name: "source", Usage: "provenance"},
			&cli.StringFlag{Name: "freshness", Usage: "one-line currency/trust note"},
			&cli.StringFlag{Name: "body", Usage: "note body"},
			&cli.BoolFlag{Name: "stdin", Usage: "read the body from standard input"},
			&cli.BoolFlag{Name: "force", Usage: "overwrite an existing note"},
			&cli.BoolFlag{Name: "no-commit", Usage: "do not git-commit the new note"},
			&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "commit message"},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			target, err := sc.writeTarget()
			if err != nil {
				return err
			}
			body := cmd.String("body")
			if cmd.Bool("stdin") {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				body = string(data)
			}
			title := cmd.String("title")
			if title == "" {
				title = cmd.String("path")
			}
			// Best-effort, before the write so the new note can't match itself.
			dups, _ := target.NearDuplicates(title, cmd.String("summary"), 0.6, 3)
			rel, err := target.Write(brain.WriteParams{
				Path:      cmd.String("path"),
				Dir:       cmd.String("dir"),
				Title:     cmd.String("title"),
				Type:      cmd.String("type"),
				Status:    cmd.String("status"),
				Pinned:    cmd.Bool("pinned"),
				Summary:   cmd.String("summary"),
				Tags:      splitCSV(cmd.StringSlice("tags")),
				Source:    cmd.String("source"),
				Freshness: cmd.String("freshness"),
				Body:      body,
				Force:     cmd.Bool("force"),
			})
			if err != nil {
				return err
			}
			fmt.Printf("wrote %s:%s\n", target.Name, rel)
			for i, d := range dups {
				if i == 0 {
					fmt.Fprintln(os.Stderr, "warning: similar notes already exist — one fact per file; prefer `multi append` over a duplicate:")
				}
				fmt.Fprintf(os.Stderr, "  %.2f  %s | %s\n", d.Score, d.Path, d.Summary)
			}
			return maybeCommit(target, cmd, []string{rel}, "note: "+rel)
		},
	}
}

func appendCmd() *cli.Command {
	return &cli.Command{
		Name:      "append",
		Usage:     "append content to a note's body and/or update its summary, freshness or retrieved (auto-committed)",
		ArgsUsage: "<note>",
		Flags: withBrain(
			&cli.StringFlag{Name: "content", Aliases: []string{"c"}, Usage: "content to append"},
			&cli.BoolFlag{Name: "stdin", Usage: "read content from standard input"},
			&cli.StringFlag{Name: "summary", Usage: "replace the note's one-line summary"},
			&cli.StringFlag{Name: "freshness", Usage: "replace the note's freshness"},
			&cli.StringFlag{Name: "retrieved", Usage: "replace the note's retrieved date"},
			&cli.BoolFlag{Name: "no-commit"},
			&cli.StringFlag{Name: "message", Aliases: []string{"m"}},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			sc, err := resolveScope(cmd)
			if err != nil {
				return err
			}
			sb, rel, err := sc.resolveNote(cmd.Args().First())
			if err != nil {
				return err
			}
			content := cmd.String("content")
			if cmd.Bool("stdin") {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				content = string(data)
			}
			fields := map[string]string{}
			for _, k := range brain.UpdatableFields {
				if cmd.IsSet(k) {
					fields[k] = cmd.String(k)
				}
			}
			hasContent := strings.TrimSpace(content) != ""
			if !hasContent && len(fields) == 0 {
				return errors.New("nothing to do: pass --content, --stdin, --summary, --freshness or --retrieved")
			}
			if len(fields) > 0 {
				if err := sb.UpdateFields(rel, fields); err != nil {
					return err
				}
				fmt.Printf("updated front matter of %s:%s\n", sb.Name, rel)
			}
			if !hasContent {
				return maybeCommit(sb, cmd, []string{rel}, "update: "+rel)
			}
			if err := sb.Append(rel, content); err != nil {
				return err
			}
			fmt.Printf("appended to %s:%s\n", sb.Name, rel)
			return maybeCommit(sb, cmd, []string{rel}, "append: "+rel)
		},
	}
}

// maybeCommit commits the given paths in the brain unless --no-commit was set or
// the brain is not a git repo.
func maybeCommit(sb ScopedBrain, cmd *cli.Command, paths []string, defaultMsg string) error {
	if cmd.Bool("no-commit") || !sb.IsGit() {
		return nil
	}
	msg := cmd.String("message")
	if msg == "" {
		msg = defaultMsg
	}
	if err := sb.Commit(paths, msg); err != nil {
		return fmt.Errorf("write succeeded but commit failed: %w", err)
	}
	return nil
}

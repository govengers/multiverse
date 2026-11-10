package ingest

import (
	"fmt"
	"strings"

	"github.com/mxcd/multiverse/internal/brain"
)

// FilingRules returns the body of the note the target brain names as
// filing_rules in its brain.yaml, without the note's H1, or "" when it names
// none. A named note that cannot be read is an error: falling back to the
// built-in rules would file notes by a layout the brain does not use.
func FilingRules() (string, error) {
	dir, err := BrainDir()
	if err != nil {
		return "", err
	}
	return filingRulesAt(dir)
}

func filingRulesAt(dir string) (string, error) {
	b, err := brain.Open(dir)
	if err != nil {
		return "", err
	}
	name := b.Settings.FilingRules
	if name == "" {
		return "", nil
	}
	rel, err := b.Resolve(name)
	if err != nil {
		return "", fmt.Errorf("filing_rules note %q: %w", name, err)
	}
	n, err := b.Load(rel)
	if err != nil {
		return "", fmt.Errorf("filing_rules note %q: %w", name, err)
	}
	body := strings.TrimSpace(n.Body)
	if strings.HasPrefix(body, "# ") {
		_, body, _ = strings.Cut(body, "\n")
		body = strings.TrimSpace(body)
	}
	if body == "" {
		return "", fmt.Errorf("filing_rules note %q is empty", name)
	}
	return body, nil
}

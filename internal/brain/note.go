package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontMatter is the known schema every note follows. Unknown keys present in a
// note on disk are preserved on read via RawFM, but only these fields are
// addressable structurally.
type FrontMatter struct {
	Type          string   `yaml:"type,omitempty"`
	Status        string   `yaml:"status,omitempty"`
	Pinned        bool     `yaml:"pinned,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
	Created       string   `yaml:"created,omitempty"`
	Updated       string   `yaml:"updated,omitempty"`
	Summary       string   `yaml:"summary,omitempty"`
	Source        string   `yaml:"source,omitempty"`
	SourceCreated string   `yaml:"source_created,omitempty"`
	SourceUpdated string   `yaml:"source_updated,omitempty"`
	Retrieved     string   `yaml:"retrieved,omitempty"`
	Freshness     string   `yaml:"freshness,omitempty"`
	Aliases       []string `yaml:"aliases,omitempty"`
}

// Note is a parsed markdown note.
type Note struct {
	Rel   string // vault-relative path, slash-separated
	FM    FrontMatter
	RawFM string // raw front-matter block text, fences excluded
	FMErr error  // why RawFM did not parse as YAML; FM is then empty
	Body  string
	HasFM bool
}

// Load reads and parses the note at the given vault-relative path.
func (b *Brain) Load(rel string) (*Note, error) {
	data, err := os.ReadFile(filepath.Join(b.Root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	return parseNote(filepath.ToSlash(rel), data), nil
}

func parseNote(rel string, data []byte) *Note {
	n := &Note{Rel: rel}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && strings.TrimRight(lines[0], "\r") == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], "\r") == "---" {
				block := strings.Join(lines[1:i], "\n")
				n.RawFM = block
				n.HasFM = true
				n.FMErr = yaml.Unmarshal([]byte(block), &n.FM)
				n.Body = strings.Join(lines[i+1:], "\n")
				return n
			}
		}
	}
	n.Body = string(data)
	return n
}

// Render serializes the front matter back to text with a stable field order and
// inline tag/alias lists, matching the vault aesthetic.
func (fm FrontMatter) Render() string {
	var b strings.Builder
	b.WriteString("---\n")
	scalar := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, yamlScalar(v))
		}
	}
	list := func(k string, vs []string) {
		if len(vs) > 0 {
			fmt.Fprintf(&b, "%s: [%s]\n", k, strings.Join(vs, ", "))
		}
	}
	scalar("type", fm.Type)
	scalar("status", fm.Status)
	if fm.Pinned {
		b.WriteString("pinned: true\n")
	}
	list("tags", fm.Tags)
	scalar("created", fm.Created)
	scalar("updated", fm.Updated)
	scalar("summary", fm.Summary)
	scalar("source", fm.Source)
	scalar("source_created", fm.SourceCreated)
	scalar("source_updated", fm.SourceUpdated)
	scalar("retrieved", fm.Retrieved)
	scalar("freshness", fm.Freshness)
	list("aliases", fm.Aliases)
	b.WriteString("---\n")
	return b.String()
}

// yamlScalar quotes a value when it would otherwise be ambiguous or invalid as a
// bare YAML scalar.
func yamlScalar(s string) string {
	needsQuote := strings.ContainsAny(s, ":#\"\n\t") ||
		strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") ||
		strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") ||
		strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "&") ||
		strings.HasPrefix(s, "*") || strings.HasPrefix(s, "!")
	if !needsQuote {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

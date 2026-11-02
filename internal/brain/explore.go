package brain

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

// ExploreOptions bounds one explore call.
type ExploreOptions struct {
	Top  int // hits taken from each ranked list (search, similar) per query
	Hops int // link-expansion depth from the seeds
}

// ExploreHit is a note reached by explore: Hop 0 was retrieved by a query,
// Hop n sits n outgoing wikilinks away from a seed.
type ExploreHit struct {
	NoteInfo
	Hop  int    `json:"hop"`
	Body string `json:"body,omitempty"`
}

// exploreFanout is the out-degree above which a note counts as a hub: it is
// reached like any other note but never expanded, like a MOC.
// ponytail: one constant; make it a brain setting if a brain's hubs differ.
const exploreFanout = 30

// Explore is the one-call retrieval for a task. Every query goes through
// search and (when indexed) similar; the union of their top hits seeds a
// bounded walk along outgoing wikilinks. Seeds rank by reciprocal-rank fusion
// across all lists; a linked note takes half of its best parent's score times
// the usual front-matter weight, and seeds always sort before linked notes. No model in the loop: the caller reads
// summaries first, bodies second.
func (b *Brain) Explore(queries []string, opt ExploreOptions) ([]ExploreHit, error) {
	score := map[string]float64{}
	hop := map[string]int{}
	fuse := func(list []NoteInfo) {
		for i, n := range list {
			if i >= opt.Top {
				break
			}
			score[n.Path] += 1 / float64(60+i)
			hop[n.Path] = 0
		}
	}
	for _, q := range queries {
		hits, err := b.Search(q, false)
		if err != nil {
			return nil, err
		}
		fuse(hits)
	}
	if b.Embeddings() != nil {
		lists, err := b.similarAll(queries, opt.Top)
		if err != nil && !errors.Is(err, ErrNoIndex) {
			return nil, err
		}
		for _, l := range lists {
			fuse(l)
		}
	}
	if len(score) == 0 {
		return nil, nil
	}

	// The link graph over the whole brain, targets resolved on slug keys like
	// Backlinks does, so a link written in any casing still connects.
	rels, err := b.Notes()
	if err != nil {
		return nil, err
	}
	loaded := map[string]*Note{}
	byKey := map[string]string{}
	for _, rel := range rels {
		n, err := b.Load(rel)
		if err != nil {
			return nil, err
		}
		loaded[rel] = n
		key := slugKey(strings.TrimSuffix(filepath.Base(rel), ".md"))
		if _, dup := byKey[key]; !dup {
			byKey[key] = rel
		}
	}

	frontier := make([]string, 0, len(hop))
	for rel := range hop {
		frontier = append(frontier, rel)
	}
	sort.Strings(frontier)
	for d := 1; d <= opt.Hops && len(frontier) > 0; d++ {
		var next []string
		for _, rel := range frontier {
			n := loaded[rel]
			if n == nil || n.FM.Type == "moc" {
				continue
			}
			links := extractLinks(n.Body)
			if len(links) > exploreFanout {
				continue
			}
			for _, l := range links {
				t, ok := byKey[slugKey(l)]
				if !ok || t == rel {
					continue
				}
				if _, seen := hop[t]; !seen {
					hop[t] = d
					next = append(next, t)
				}
				if s := score[rel] * 0.5 * rankWeight(&loaded[t].FM); hop[t] == d && s > score[t] {
					score[t] = s
				}
			}
		}
		frontier = next
	}

	out := make([]ExploreHit, 0, len(score))
	for rel, s := range score {
		info := NoteInfo{Path: rel}
		if n := loaded[rel]; n != nil {
			info = b.info(n)
		}
		info.Score = s
		out = append(out, ExploreHit{NoteInfo: info, Hop: hop[rel]})
	}
	SortExplore(out)
	return out, nil
}

// SortExplore orders hits best-first: seeds before linked notes (a query hit
// outranks any neighbor), then score descending, path as tie-break.
func SortExplore(hits []ExploreHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Hop != hits[j].Hop {
			return hits[i].Hop < hits[j].Hop
		}
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})
}

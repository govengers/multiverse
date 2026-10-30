package brain

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EmbedSettings configures the optional semantic shadow index: an OpenAI-
// compatible /v1/embeddings endpoint (Ollama, LM Studio, OpenAI, vLLM — all
// speak it). The markdown stays the source of truth; the index is a derived,
// disposable cache that `multi reindex` regenerates.
type EmbedSettings struct {
	// BaseURL is the API root, e.g. http://localhost:11434/v1 for Ollama.
	BaseURL string `yaml:"base_url"`
	// Model is the embedding model name the endpoint serves.
	Model string `yaml:"model"`
	// APIKeyEnv names the env var holding the bearer token, if the endpoint
	// needs one (local Ollama does not).
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	// QueryPrefix/DocPrefix are prepended to query/document texts — some
	// models (e.g. nomic-embed-text) require task prefixes for good recall.
	QueryPrefix string `yaml:"query_prefix,omitempty"`
	DocPrefix   string `yaml:"doc_prefix,omitempty"`
	// BatchSize caps texts per embeddings request (default 64).
	BatchSize int `yaml:"batch_size,omitempty"`
}

// Embeddings is the brain's embedding settings as they apply on this
// machine: the brain's own, with what LocalEmbeddings sets in their place.
// Nil when neither configures any.
func (b *Brain) Embeddings() *EmbedSettings {
	return b.LocalEmbeddings.Over(b.Settings.Embeddings)
}

// Over returns base with every field e sets in its place; base itself when e
// is nil, e alone when base is.
func (e *EmbedSettings) Over(base *EmbedSettings) *EmbedSettings {
	if e == nil {
		return base
	}
	out := EmbedSettings{}
	if base != nil {
		out = *base
	}
	if e.BaseURL != "" {
		out.BaseURL = e.BaseURL
	}
	if e.Model != "" {
		out.Model = e.Model
	}
	if e.APIKeyEnv != "" {
		out.APIKeyEnv = e.APIKeyEnv
	}
	if e.QueryPrefix != "" {
		out.QueryPrefix = e.QueryPrefix
	}
	if e.DocPrefix != "" {
		out.DocPrefix = e.DocPrefix
	}
	if e.BatchSize > 0 {
		out.BatchSize = e.BatchSize
	}
	return &out
}

// ErrNoEmbeddings marks a brain without embeddings configured.
var ErrNoEmbeddings = errors.New("embeddings are not configured for this brain (add an `embeddings:` block to .multi/brain.yaml)")

// ErrNoIndex marks a missing/empty shadow index.
var ErrNoIndex = errors.New("no embedding index yet — run `multi reindex` first")

// embedIndex is the on-disk shadow index: one normalized vector per note,
// keyed by vault-relative path. It lives in the user cache dir, never in the
// brain repo — delete it any time, `multi reindex` rebuilds it.
type embedIndex struct {
	Model   string
	Entries map[string]embedEntry
}

type embedEntry struct {
	Hash   string
	Vector []float32
}

// ReindexStats reports what a reindex run did.
type ReindexStats struct {
	Embedded int // notes (re-)embedded this run
	Kept     int // notes whose vectors were still current
	Removed  int // stale entries for deleted notes
}

func (b *Brain) embedIndexPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(b.Root))
	return filepath.Join(cache, "multi", "embed-"+hex.EncodeToString(sum[:6])+".gob"), nil
}

func (b *Brain) loadEmbedIndex() *embedIndex {
	idx := &embedIndex{Entries: map[string]embedEntry{}}
	p, err := b.embedIndexPath()
	if err != nil {
		return idx
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return idx
	}
	_ = gob.NewDecoder(bytes.NewReader(data)).Decode(idx)
	if idx.Entries == nil {
		idx.Entries = map[string]embedEntry{}
	}
	return idx
}

func (b *Brain) saveEmbedIndex(idx *embedIndex) error {
	p, err := b.embedIndexPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(idx); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

// embedText is the note representation that gets embedded: path, summary and
// tags carry the distilled signal, the body (truncated) the detail. One vector
// per note — the one-fact-per-file discipline already did the chunking.
func embedText(n *Note) string {
	const bodyLimit = 4000
	body := n.Body
	if len(body) > bodyLimit {
		body = strings.ToValidUTF8(body[:bodyLimit], "")
	}
	return n.Rel + "\n" + n.FM.Summary + "\n" + strings.Join(n.FM.Tags, " ") + "\n" + body
}

// Reindex brings the shadow index up to date: unchanged notes keep their
// vectors (matched by content hash), changed and new notes are re-embedded in
// batches, entries for deleted notes are dropped. force discards everything
// first. progress, when non-nil, receives one line per batch.
func (b *Brain) Reindex(force bool, progress func(string)) (ReindexStats, error) {
	es := b.Embeddings()
	if es == nil {
		return ReindexStats{}, ErrNoEmbeddings
	}
	idx := b.loadEmbedIndex()
	if force || idx.Model != es.Model {
		idx = &embedIndex{Model: es.Model, Entries: map[string]embedEntry{}}
	}
	notes, err := b.Notes()
	if err != nil {
		return ReindexStats{}, err
	}

	var stats ReindexStats
	seen := map[string]bool{}
	var pendingRels, pendingTexts, pendingHashes []string
	for _, rel := range notes {
		n, err := b.Load(rel)
		if err != nil {
			return stats, err
		}
		seen[rel] = true
		text := es.DocPrefix + embedText(n)
		sum := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(sum[:])
		if e, ok := idx.Entries[rel]; ok && e.Hash == hash {
			stats.Kept++
			continue
		}
		pendingRels = append(pendingRels, rel)
		pendingTexts = append(pendingTexts, text)
		pendingHashes = append(pendingHashes, hash)
	}
	for rel := range idx.Entries {
		if !seen[rel] {
			delete(idx.Entries, rel)
			stats.Removed++
		}
	}

	batch := es.BatchSize
	if batch <= 0 {
		batch = 64
	}
	for start := 0; start < len(pendingTexts); start += batch {
		end := min(start+batch, len(pendingTexts))
		vecs, err := es.embed(pendingTexts[start:end])
		if err != nil {
			return stats, err
		}
		for i, v := range vecs {
			idx.Entries[pendingRels[start+i]] = embedEntry{Hash: pendingHashes[start+i], Vector: normalize(v)}
			stats.Embedded++
		}
		if progress != nil {
			progress(fmt.Sprintf("embedded %d/%d", end, len(pendingTexts)))
		}
	}
	return stats, b.saveEmbedIndex(idx)
}

// Similar returns the topK notes semantically closest to the query. Brute
// force over the whole index — at vault scale that is faster than any ANN
// structure would ever pay for. Scores are cosine similarity scaled by
// statusWeight, so deprecated knowledge ranks below what superseded it.
func (b *Brain) Similar(query string, topK int) ([]NoteInfo, error) {
	lists, err := b.similarAll([]string{query}, topK)
	if err != nil {
		return nil, err
	}
	return lists[0], nil
}

// similarAll runs Similar for several queries with a single embedding call:
// one HTTP round trip is the whole cost of a query at vault scale.
func (b *Brain) similarAll(queries []string, topK int) ([][]NoteInfo, error) {
	es := b.Embeddings()
	if es == nil {
		return nil, ErrNoEmbeddings
	}
	texts := make([]string, len(queries))
	for i, q := range queries {
		texts[i] = es.QueryPrefix + q
	}
	vecs, err := es.embed(texts)
	if err != nil {
		return nil, err
	}
	out := make([][]NoteInfo, len(queries))
	for i, v := range vecs {
		if out[i], err = b.nearest(normalize(v), topK, "", true); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SimilarNote returns the topK nearest neighbors of an already-indexed note —
// the primitive behind dedup and "related notes" during grooming. Scores stay
// raw cosine: dedup must see deprecated near-duplicates at full strength.
func (b *Brain) SimilarNote(rel string, topK int) ([]NoteInfo, error) {
	if b.Embeddings() == nil {
		return nil, ErrNoEmbeddings
	}
	idx := b.loadEmbedIndex()
	e, ok := idx.Entries[rel]
	if !ok {
		return nil, fmt.Errorf("%s is not in the embedding index — run `multi reindex`", rel)
	}
	return b.nearest(e.Vector, topK, rel, false)
}

func (b *Brain) nearest(query []float32, topK int, exclude string, demote bool) ([]NoteInfo, error) {
	idx := b.loadEmbedIndex()
	if len(idx.Entries) == 0 {
		return nil, ErrNoIndex
	}
	out := make([]NoteInfo, 0, len(idx.Entries))
	for rel, e := range idx.Entries {
		if rel == exclude {
			continue
		}
		info := NoteInfo{Path: rel, Score: dot(query, e.Vector)}
		// A hit may be stale (note deleted since last reindex); keep the path
		// but skip the front matter rather than failing the whole query.
		if n, err := b.Load(rel); err == nil {
			score := info.Score
			info = b.info(n)
			info.Score = score
			if demote {
				info.Score *= statusWeight(n.FM.Status)
			}
		}
		out = append(out, info)
	}
	SortByScore(out)
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

// EmbedIndexStatus reports the shadow index's coverage and age: how many notes
// carry a vector vs. how many exist on disk, and when the index file last
// changed. Coverage by count catches new and deleted notes; edited notes with
// stale vectors only surface on reindex (hashing every note here would cost a
// full scan).
type EmbedIndexStatus struct {
	Indexed int
	Notes   int
	Updated time.Time
}

func (b *Brain) EmbedIndexStatus() (EmbedIndexStatus, error) {
	var st EmbedIndexStatus
	if b.Embeddings() == nil {
		return st, ErrNoEmbeddings
	}
	if p, err := b.embedIndexPath(); err == nil {
		if fi, err := os.Stat(p); err == nil {
			st.Updated = fi.ModTime()
		}
	}
	st.Indexed = len(b.loadEmbedIndex().Entries)
	notes, err := b.Notes()
	if err != nil {
		return st, err
	}
	st.Notes = len(notes)
	return st, nil
}

// embed calls the OpenAI-compatible embeddings endpoint for a batch of texts.
func (es *EmbedSettings) embed(texts []string) ([][]float32, error) {
	if es.BaseURL == "" || es.Model == "" {
		return nil, errors.New("embeddings config needs base_url and model")
	}
	payload, err := json.Marshal(map[string]any{"model": es.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(es.BaseURL, "/") + "/embeddings"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if es.APIKeyEnv != "" {
		if key := os.Getenv(es.APIKeyEnv); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings endpoint %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint %s: %s: %s", url, resp.Status, truncate(string(body), 200))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("embeddings response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings response: got %d vectors for %d texts", len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embeddings response: index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// normalize L2-normalizes a vector in place so dot product equals cosine.
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := 1 / math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) * inv)
	}
	return v
}

func dot(a, b []float32) float64 {
	n := min(len(a), len(b))
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

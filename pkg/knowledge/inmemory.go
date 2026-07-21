package knowledge

import (
	"context"
	"math"
	"strings"
	"sync"
	"unicode"
)

// InMemoryStore is a goroutine-safe, in-memory knowledge store.
//
// It supports two search modes:
//  1. Keyword search (default) — simple TF (term frequency) scoring
//     against tokenised document content.
//  2. Semantic search (optional) — cosine similarity over embedding vectors.
//     Provide an EmbedFn to enable this. When EmbedFn is set, Store()
//     pre-computes the embedding for each document automatically, and Search()
//     prioritises semantic results.
type InMemoryStore struct {
	mu      sync.RWMutex
	docs    map[string]*Document // id → Document
	embedFn func(ctx context.Context, text string) ([]float32, error)
}

// NewInMemoryStore creates an empty InMemoryStore with keyword-only search.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		docs: make(map[string]*Document),
	}
}

// NewInMemoryStoreWithEmbedding creates an InMemoryStore that uses the
// given embedding function for semantic search. When embedFn is nil it
// falls back to keyword-only mode.
func NewInMemoryStoreWithEmbedding(embedFn func(ctx context.Context, text string) ([]float32, error)) *InMemoryStore {
	return &InMemoryStore{
		docs:    make(map[string]*Document),
		embedFn: embedFn,
	}
}

var _ Store = (*InMemoryStore)(nil)

// Store inserts or updates documents. When embedFn is configured, each
// document's Content is embedded automatically (unless it already has a
// non-nil Embedding).
func (s *InMemoryStore) Store(ctx context.Context, docs ...*Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, doc := range docs {
		if doc.ID == "" {
			continue
		}
		// Deep-copy the document so external mutations don't affect the store.
		emb := copyEmbedding(doc.Embedding)
		// Auto-embed when embedFn is set and document has no embedding yet.
		if s.embedFn != nil && len(emb) == 0 {
			var err error
			emb, err = s.embedFn(ctx, doc.Content)
			if err != nil {
				return err
			}
		}
		cp := &Document{
			ID:        doc.ID,
			Content:   doc.Content,
			Metadata:  copyMetadata(doc.Metadata),
			Embedding: emb,
		}
		s.docs[cp.ID] = cp
	}
	return nil
}

// Search returns documents relevant to the query string.
//
// When embedFn is configured, Search embeds the query on every call and
// returns results sorted by cosine similarity. Otherwise it uses TF-based
// keyword scoring against the content tokens.
func (s *InMemoryStore) Search(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	params := applySearchOptions(opts)

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.docs) == 0 {
		return nil, nil
	}

	if s.embedFn != nil {
		return s.semanticSearch(ctx, query, params)
	}
	return s.keywordSearch(query, params)
}

func (s *InMemoryStore) semanticSearch(ctx context.Context, query string, params SearchParams) ([]*Result, error) {
	queryEmb, err := s.embedFn(ctx, query)
	if err != nil {
		return nil, err
	}

	results := make([]*Result, 0, len(s.docs))
	for _, doc := range s.docs {
		if len(doc.Embedding) == 0 {
			continue
		}
		sim := cosineSimilarity(queryEmb, doc.Embedding)
		if sim < params.Threshold {
			continue
		}
		results = append(results, &Result{
			Document: *doc,
			Score:    sim,
		})
	}

	sortResults(results)
	if len(results) > params.TopK {
		results = results[:params.TopK]
	}
	return results, nil
}

func (s *InMemoryStore) keywordSearch(query string, params SearchParams) ([]*Result, error) {
	queryTokens := tokenise(query)
	if len(queryTokens) == 0 {
		return nil, nil
	}

	results := make([]*Result, 0, len(s.docs))
	for _, doc := range s.docs {
		docTokens := tokenise(doc.Content)
		if len(docTokens) == 0 {
			continue
		}

		// Simple TF scoring: count how many query tokens appear in the doc,
		// normalised by doc token count.
		var matchCount int
		for _, qt := range queryTokens {
			for _, dt := range docTokens {
				if strings.EqualFold(qt, dt) {
					matchCount++
					break
				}
			}
		}
		score := float64(matchCount) / float64(len(queryTokens)+len(docTokens)-matchCount)

		if score < params.Threshold {
			continue
		}
		results = append(results, &Result{
			Document: *doc,
			Score:    score,
		})
	}

	sortResults(results)
	if len(results) > params.TopK {
		results = results[:params.TopK]
	}
	return results, nil
}

// Delete removes documents by ID. Non-existent IDs are silently ignored.
func (s *InMemoryStore) Delete(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.docs, id)
	}
	return nil
}

// Close is a no-op for InMemoryStore. Implements Store.
func (s *InMemoryStore) Close() error { return nil }

// --- helpers ---------------------------------------------------------------

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func tokenise(s string) []string {
	var tokens []string
	var buf strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(r)
		} else {
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

func sortResults(results []*Result) {
	// Simple insertion sort — n is typically very small (TopK ≤ 10).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func copyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func copyEmbedding(e []float32) []float32 {
	if e == nil {
		return nil
	}
	cp := make([]float32, len(e))
	copy(cp, e)
	return cp
}

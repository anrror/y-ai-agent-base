package knowledge

import (
	"context"
	"sync"
)

// HybridRetriever fans out a query to multiple Retrievers in parallel,
// merges and deduplicates results, and returns them sorted by score.
//
// Use it to combine local document search with web search:
//
//	hybrid := knowledge.NewHybridRetriever(
//	    knowledge.NewStoreRetriever("docs", myStore),
//	    webSearchRetriever,
//	)
type HybridRetriever struct {
	id         string
	retrievers []Retriever
}

// NewHybridRetriever creates a HybridRetriever that queries all given
// retrievers in parallel. Results are merged, deduplicated by ID, and
// sorted by descending score.
func NewHybridRetriever(id string, retrievers ...Retriever) *HybridRetriever {
	if id == "" {
		id = "hybrid"
	}
	return &HybridRetriever{id: id, retrievers: retrievers}
}

func (h *HybridRetriever) ID() string { return h.id }

// Add appends additional retrievers. Safe to call before first Retrieve.
func (h *HybridRetriever) Add(retrievers ...Retriever) {
	h.retrievers = append(h.retrievers, retrievers...)
}

// Retrieve fans out to all retrievers in parallel, merges results by ID,
// deduplicates (keeping highest score per ID), and returns the top results
// sorted by descending score.
func (h *HybridRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	if len(h.retrievers) == 0 {
		return nil, nil
	}

	params := applySearchOptions(opts)

	type retrieverResult struct {
		results []*Result
		err     error
	}

	// Fan out to all retrievers in parallel.
	ch := make(chan retrieverResult, len(h.retrievers))
	var wg sync.WaitGroup

	for _, r := range h.retrievers {
		wg.Add(1)
		r := r // capture
		go func() {
			defer wg.Done()
			results, err := r.Retrieve(ctx, query, opts...)
			ch <- retrieverResult{results: results, err: err}
		}()
	}

	wg.Wait()
	close(ch)

	// Merge and deduplicate by ID (keep highest score per ID).
	dedup := make(map[string]*Result)
	for rr := range ch {
		if rr.err != nil {
			continue // skip errored retrievers
		}
		for _, res := range rr.results {
			if res == nil {
				continue
			}
			if existing, ok := dedup[res.ID]; ok {
				if res.Score > existing.Score {
					dedup[res.ID] = res
				}
			} else {
				dedup[res.ID] = res
			}
		}
	}

	// Collect and sort.
	merged := make([]*Result, 0, len(dedup))
	for _, res := range dedup {
		if res.Score >= params.Threshold {
			merged = append(merged, res)
		}
	}
	sortResults(merged)
	if len(merged) > params.TopK {
		merged = merged[:params.TopK]
	}
	return merged, nil
}

// compile-time interface check.
var _ Retriever = (*HybridRetriever)(nil)

package knowledge

import "context"

// Retriever is the composable abstraction for knowledge retrieval.
//
// Unlike Store (which couples storage + retrieval), a Retriever represents
// a single retrieval strategy or source. Multiple Retrievers can be
// combined into a HybridRetriever or attached directly to a Knowledge
// component.
//
// Built-in implementations:
//   - StoreRetriever    — wraps any Store as a Retriever
//   - WebSearchRetriever — searches the internet via a provided function
//   - WebFetchRetriever  — fetches and parses a URL
//   - HybridRetriever    — fans out to multiple Retrievers in parallel
type Retriever interface {
	// ID is a unique identifier for this retriever, e.g. "web_search",
	// "docs_store", "hybrid". Used for deduplication in merges.
	ID() string

	// Retrieve returns documents relevant to the query.
	// The implementation decides the retrieval algorithm; results should be
	// sorted by descending relevance score.
	Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error)
}

// StoreRetriever wraps a Store as a Retriever.
type StoreRetriever struct {
	id    string
	store Store
}

// NewStoreRetriever creates a Retriever backed by the given Store.
// Use a custom id to distinguish multiple stores (e.g. "docs", "wiki").
func NewStoreRetriever(id string, store Store) *StoreRetriever {
	if id == "" {
		id = "store"
	}
	return &StoreRetriever{id: id, store: store}
}

func (r *StoreRetriever) ID() string { return r.id }

func (r *StoreRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error) {
	if r.store == nil {
		return nil, nil
	}
	return r.store.Search(ctx, query, opts...)
}

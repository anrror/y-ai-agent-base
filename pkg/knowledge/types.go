// Package knowledge defines a pluggable, per-agent knowledge system
// with multi-source retrieval, internet search, URL fetching, and
// automatic tool registration.
//
// # Architecture
//
// Three core abstractions:
//   - Store        — knowledge storage backend (CRUD)
//   - Retriever    — single-source retrieval strategy
//   - Knowledge    — component that combines multiple Retrievers and
//     exposes them as agent-callable tools
//
// # Integration
//
// Each Agent may optionally attach a Knowledge component via the Builder:
//
//	ag, err := ac.ToBuilder().
//	    WithProvider(prov).
//	    WithPipeline(pipe).
//	    WithKnowledge(knowledge.New(myStore, knowledge.DefaultConfig())).
//	    Build()
//
// When Knowledge is attached, Agent.Knowledge() returns the component;
// otherwise it returns nil. This makes knowledge fully pluggable and
// selectable per Agent.
//
// # Integration Modes
//
// Two integration modes are supported (can be used together):
//
//  1. Auto-Inject (via pipeline middleware) — when Knowledge has AutoInject
//     enabled, relevant knowledge is automatically retrieved and injected
//     into the system prompt on every Chat() call. Knowledge implements
//     MiddlewareProvider so the middleware is auto-wired at build time.
//
//  2. Manual / Tool-based — the LLM autonomously calls tools that the
//     Knowledge component registers on the Agent. Three tools are
//     available depending on which Retrievers are configured:
//   - search_knowledge  — queries all registered Retrievers (always available)
//   - search_web        — internet search (requires WebSearchRetriever)
//   - fetch_url         — URL content fetch (requires WebFetchRetriever)
//
// Knowledge implements ToolProvider, so tools are auto-registered during
// Build() without explicit registration.
//
// # Built-in Retrievers
//
//   - StoreRetriever      — wraps any Store as a Retriever
//   - WebSearchRetriever  — internet search via caller-provided function
//   - WebFetchRetriever   — URL fetch via caller-provided function
//   - HybridRetriever     — parallel fan-out to multiple Retrievers,
//     merge, dedup by ID, sort by descending score
//
// # Multi-Source Search
//
// Knowledge.Search() automatically fans out to all registered Retrievers
// and merges results. Single Retriever → direct delegation. Multiple
// Retrievers → HybridRetriever with parallel fan-out.
package knowledge

// Document represents a single knowledge entry (a chunk of text with
// optional metadata and embedding). Embedding is left as a raw []float32
// so the caller can pre-compute it with their chosen embedding provider.
type Document struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Embedding []float32      `json:"-"`
}

// Result holds a matched Document together with its relevance score.
type Result struct {
	Document
	Score float64 `json:"score"`
}

// DefaultSearchParams are the defaults used when no SearchOption is given.
var DefaultSearchParams = SearchParams{
	TopK:      5,
	Threshold: 0.0,
}

// SearchParams holds parameters for a knowledge search.
type SearchParams struct {
	TopK      int
	Threshold float64
}

// SearchOption is a functional option for Store.Search.
type SearchOption func(*SearchParams)

// WithTopK sets the maximum number of search results to return.
// Values ≤ 0 are ignored (the default or previous value is kept).
func WithTopK(k int) SearchOption {
	return func(p *SearchParams) {
		if k > 0 {
			p.TopK = k
		}
	}
}

// WithThreshold sets the minimum similarity threshold [0,1] for
// semantic search. Results below this threshold are discarded.
// Values ≤ 0 match everything.
func WithThreshold(t float64) SearchOption {
	return func(p *SearchParams) {
		if t > 0 {
			p.Threshold = t
		}
	}
}

// applySearchOptions applies the functional options and returns
// the effective SearchParams.
func applySearchOptions(opts []SearchOption) SearchParams {
	p := DefaultSearchParams
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

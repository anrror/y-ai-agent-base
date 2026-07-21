package knowledge

import "context"

// Store is the pluggable interface for knowledge backends.
//
// Each implementation stores and retrieves documents. The simplest
// built-in implementation is InMemoryStore.
//
// To connect to an external vector database or document store,
// implement this interface:
//
//	type MyVectorStore struct { ... }
//	func (s *MyVectorStore) Store(ctx context.Context, docs ...*knowledge.Document) error { ... }
//	func (s *MyVectorStore) Search(ctx context.Context, query string, opts ...knowledge.SearchOption) ([]*knowledge.Result, error) { ... }
//	func (s *MyVectorStore) Delete(ctx context.Context, ids ...string) error { ... }
//	func (s *MyVectorStore) Close() error { ... }
type Store interface {
	// Store inserts or updates one or more documents.
	// IDs are supplied by the caller; duplicate IDs overwrite the existing
	// document (upsert semantics).
	Store(ctx context.Context, docs ...*Document) error

	// Search finds documents relevant to the query string.
	// The backend decides the relevance algorithm (keyword, embedding
	// cosine-similarity, hybrid, etc.). Results are sorted by descending
	// relevance score.
	Search(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error)

	// Delete removes one or more documents by ID.
	// Non-existent IDs are silently ignored.
	Delete(ctx context.Context, ids ...string) error

	// Close releases resources held by the store. Idempotent.
	Close() error
}

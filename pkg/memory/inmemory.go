package memory

import (
	"context"
	"strings"
	"sync"
)

// InMemoryStore is a simple in-memory implementation of Store, intended for
// testing and development. It stores entries keyed by ID and supports
// prefix-match search on IDs and substring-match on content.
type InMemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// NewInMemoryStore returns an empty InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		entries: make(map[string]*Entry),
	}
}

// Add stores an entry. If an entry with the same ID already exists, it is
// overwritten.
func (s *InMemoryStore) Add(_ context.Context, entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[entry.ID] = entry
	return nil
}

// Search returns entries whose ID starts with query or whose Content contains
// query (case-insensitive substring match). Results are limited by limit; a
// zero or negative limit returns all matches.
func (s *InMemoryStore) Search(_ context.Context, query string, limit int) ([]*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]*Entry, 0)
	queryLower := strings.ToLower(query)

	for _, entry := range s.entries {
		if strings.HasPrefix(entry.ID, query) ||
			strings.Contains(strings.ToLower(entry.Content), queryLower) {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

// Delete removes the entry identified by id. Deleting a non-existent entry is
// a no-op.
func (s *InMemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	return nil
}

// Close is a no-op for the in-memory store.
func (s *InMemoryStore) Close() error {
	return nil
}

// Len returns the number of entries currently stored.
func (s *InMemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

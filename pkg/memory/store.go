// Package memory defines the memory backend interface.
package memory

import "context"

// Entry represents a single memory record.
type Entry struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// Store is the interface for memory backends.
type Store interface {
	Add(ctx context.Context, entry *Entry) error
	Search(ctx context.Context, query string, limit int) ([]*Entry, error)
	Delete(ctx context.Context, id string) error
	Close() error
}

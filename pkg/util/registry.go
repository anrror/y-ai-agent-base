// Package util provides shared generic utilities for the y-ai-agent-base framework.
package util

import (
	"fmt"
	"sync"
)

// Sentinel errors for Registry operations. Package-specific registries
// wrap these with their own sentinels as needed.
var (
	ErrNotFound      = fmt.Errorf("registry: not found")
	ErrAlreadyExists = fmt.Errorf("registry: already exists")
)

// Registry is a generic, thread-safe key-value store with CRUD operations.
// T can be any type, including interface types. Each package embeds
// *util.Registry[T] and adds type-specific wrappers.
type Registry[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

// NewRegistry creates an empty registry.
func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{items: make(map[string]T)}
}

// Register stores a value under the given key. Returns ErrAlreadyExists
// when a value already exists for that key.
func (r *Registry[T]) Register(id string, v T) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.items[id]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, id)
	}
	r.items[id] = v
	return nil
}

// Get retrieves a value by key. The second return value indicates
// whether the key was found.
func (r *Registry[T]) Get(id string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.items[id]
	return v, ok
}

// List returns all stored values as a slice.
func (r *Registry[T]) List() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]T, 0, len(r.items))
	for _, v := range r.items {
		result = append(result, v)
	}
	return result
}

// Delete removes a value by key. Returns ErrNotFound when the key
// does not exist.
func (r *Registry[T]) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.items[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.items, id)
	return nil
}

// Count returns the number of entries in the registry.
func (r *Registry[T]) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.items)
}

package store

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory AgentStore backed by sync.Map.
// Values are stored as JSON bytes (raw []byte) to avoid deserialization
// overhead on Save/Load round-trips, matching the contract.
// LoadAll returns the raw JSON bytes for each key as a json.RawMessage.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore returns an initialized MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string][]byte),
	}
}

// Save stores the JSON-marshalled value under key.
func (m *MemoryStore) Save(_ context.Context, key string, value any) error {
	data, err := Marshal(value)
	if err != nil {
		return wrapErr("save", err)
	}
	m.mu.Lock()
	m.data[key] = data
	m.mu.Unlock()
	return nil
}

// Load retrieves and JSON-unmarshalls the value at key into dest.
func (m *MemoryStore) Load(_ context.Context, key string, dest any) error {
	m.mu.RLock()
	data, ok := m.data[key]
	m.mu.RUnlock()
	if !ok {
		return wrapErr("load", ErrNotFound)
	}
	if err := Unmarshal(data, dest); err != nil {
		return wrapErr("load", err)
	}
	return nil
}

// LoadAll returns all stored values as raw JSON messages.
// Each element is a map[string]any decoded from the raw JSON.
func (m *MemoryStore) LoadAll(_ context.Context) ([]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]any, 0, len(m.data))
	for _, data := range m.data {
		var v any
		if err := Unmarshal(data, &v); err != nil {
			return nil, wrapErr("loadall", err)
		}
		results = append(results, v)
	}
	return results, nil
}

// Delete removes the value at key.
func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.data, key)
	m.mu.Unlock()
	return nil
}

// Close clears all data.
func (m *MemoryStore) Close() error {
	m.mu.Lock()
	m.data = nil
	m.mu.Unlock()
	return nil
}

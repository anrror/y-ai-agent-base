// Package store defines persistent storage interfaces and implementations.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Common store errors.
var (
	ErrNotFound = errors.New("store: key not found")
)

// StoreError wraps an underlying error with operation context.
type StoreError struct {
	Op  string // operation name: "save", "load", "loadall", "delete"
	Err error  // underlying error
}

func (e *StoreError) Error() string {
	return fmt.Sprintf("store: %s: %v", e.Op, e.Err)
}

func (e *StoreError) Unwrap() error {
	return e.Err
}

// wrapErr creates a StoreError if err is non-nil.
func wrapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return &StoreError{Op: op, Err: err}
}

// AgentStore is the persistent storage interface for agent configurations
// and runtime state. Values are serialized as JSON.
type AgentStore interface {
	// Save persists value under key. value is JSON-marshalled.
	Save(ctx context.Context, key string, value any) error
	// Load retrieves the value at key and JSON-unmarshalls into dest.
	// dest must be a non-nil pointer.
	Load(ctx context.Context, key string, dest any) error
	// LoadAll returns all values. Each element is JSON-unmarshalled
	// into a new instance of the type T registered via the store type.
	LoadAll(ctx context.Context) ([]any, error)
	// Delete removes the value at key.
	Delete(ctx context.Context, key string) error
	// Close releases resources held by the store.
	Close() error
}

// Marshal serializes v as JSON.
func Marshal(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("store marshal: %w", err)
	}
	return data, nil
}

// Unmarshal deserializes data into dest (must be a non-nil pointer).
func Unmarshal(data []byte, dest any) error {
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("store unmarshal: %w", err)
	}
	return nil
}

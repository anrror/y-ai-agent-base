// Package cache provides pluggable inference caching with exact-match and
// semantic-similarity strategies.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrCacheMiss is returned by Cache.Get when the requested key is not present
// or has expired.
var ErrCacheMiss = errors.New("cache: miss")

// Mode selects the caching strategy.
type Mode string

const (
	ModeExact    Mode = "exact"    // exact message-hash match
	ModeSemantic Mode = "semantic" // cosine-similarity match
)

// Key uniquely identifies a cacheable inference request.
type Key struct {
	Model       string
	Messages    string  // serialised message fingerprint
	Temperature float64
	Mode        Mode
}

// Entry holds a cached response and its metadata.
type Entry struct {
	Content   string
	CreatedAt time.Time
	ExpiresAt time.Time
	HitCount  int64
	Embedding []float32 // populated only for semantic keys
}

// Config controls cache behaviour.
type Config struct {
	Mode      Mode          // caching strategy
	TTL       time.Duration // 0 = no expiry
	MaxSize   int           // 0 = unlimited
	Threshold float64       // similarity threshold [0,1] for semantic mode
}

// Cache is the unified interface for inference result caching.
type Cache interface {
	Get(ctx context.Context, key Key) (*Entry, error)
	Set(ctx context.Context, key Key, entry *Entry) error
	Delete(ctx context.Context, key Key) error
	Clear(ctx context.Context) error
	Mode() Mode
}

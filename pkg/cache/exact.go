package cache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ExactCache is an in-memory cache keyed by SHA-256 of the serialised Key.
// Goroutine-safe.
type ExactCache struct {
	mu    sync.RWMutex
	items map[string]*entryWithDeadline
	cfg   Config
}

type entryWithDeadline struct {
	*Entry
	deadline time.Time
}

// NewExactCache creates an ExactCache with the given config.
func NewExactCache(cfg Config) *ExactCache {
	if cfg.TTL == 0 {
		cfg.TTL = 5 * time.Minute
	}
	return &ExactCache{
		items: make(map[string]*entryWithDeadline),
		cfg:   cfg,
	}
}

var _ Cache = (*ExactCache)(nil)

func (c *ExactCache) Mode() Mode { return ModeExact }

func (c *ExactCache) Get(_ context.Context, key Key) (*Entry, error) {
	k := c.key(key)
	c.mu.RLock()
	e, ok := c.items[k]
	if ok && !e.deadline.IsZero() && time.Now().After(e.deadline) {
		// Expired — upgrade to write lock and delete.
		// Re-check under write lock to avoid TOCTOU: another goroutine
		// may have refreshed this entry between RUnlock and Lock.
		c.mu.RUnlock()
		c.mu.Lock()
		if e2, ok2 := c.items[k]; ok2 && !e2.deadline.IsZero() && time.Now().After(e2.deadline) {
			delete(c.items, k)
		}
		c.mu.Unlock()
		return nil, fmt.Errorf("cache: %w", ErrCacheMiss)
	}
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cache: %w", ErrCacheMiss)
	}
	atomic.AddInt64(&e.HitCount, 1)
	return e.Entry, nil
}

func (c *ExactCache) Set(_ context.Context, key Key, entry *Entry) error {
	k := c.key(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.MaxSize > 0 && len(c.items) >= c.cfg.MaxSize {
		// Evict the entry closest to expiry (earliest deadline) to
		// retain hot entries that may still be useful.
		var evictKey string
		var earliest time.Time
		first := true
		for k, v := range c.items {
			if first || (!v.deadline.IsZero() && v.deadline.Before(earliest)) {
				evictKey = k
				earliest = v.deadline
				first = false
			}
		}
		delete(c.items, evictKey)
	}
	var deadline time.Time
	if c.cfg.TTL > 0 {
		deadline = time.Now().Add(c.cfg.TTL)
	}
	c.items[k] = &entryWithDeadline{Entry: entry, deadline: deadline}
	return nil
}

func (c *ExactCache) Delete(_ context.Context, key Key) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, c.key(key))
	return nil
}

func (c *ExactCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*entryWithDeadline)
	return nil
}

func (c *ExactCache) key(k Key) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%f", k.Model, k.Messages, k.Temperature)))
	return string(h[:])
}

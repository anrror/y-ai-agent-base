package cache

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// SemanticCache is an in-memory cache that matches by cosine similarity
// between message embeddings. Goroutine-safe.
type SemanticCache struct {
	mu       sync.RWMutex
	entries  []*semanticEntry
	cfg      Config
	embedFn  func(ctx context.Context, text string) ([]float32, error)
}

type semanticEntry struct {
	key      string   // original Key.Messages
	embedding []float32
	entry    *Entry
	deadline time.Time
}

// NewSemanticCache creates a SemanticCache.
// embedFn is required — it produces the embedding vectors used for similarity.
func NewSemanticCache(cfg Config, embedFn func(ctx context.Context, text string) ([]float32, error)) *SemanticCache {
	if cfg.TTL == 0 {
		cfg.TTL = 5 * time.Minute
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.92
	}
	return &SemanticCache{cfg: cfg, embedFn: embedFn}
}

var _ Cache = (*SemanticCache)(nil)

func (c *SemanticCache) Mode() Mode { return ModeSemantic }

func (c *SemanticCache) Get(ctx context.Context, key Key) (*Entry, error) {
	if c.embedFn == nil {
		return nil, fmt.Errorf("cache: %w", ErrCacheMiss)
	}
	queryEmb, err := c.embedFn(ctx, key.Messages)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	var bestEntry *Entry
	var bestSim float64

	for _, e := range c.entries {
		if !e.deadline.IsZero() && now.After(e.deadline) {
			continue
		}
		sim := cosineSimilarity(queryEmb, e.embedding)
		if sim > bestSim {
			bestSim = sim
			bestEntry = e.entry

		}
	}

	if bestSim >= c.cfg.Threshold && bestEntry != nil {
		return bestEntry, nil
	}
	return nil, fmt.Errorf("cache: %w", ErrCacheMiss)
}

func (c *SemanticCache) Set(_ context.Context, key Key, entry *Entry) error {
	if c.embedFn == nil || len(entry.Embedding) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var deadline time.Time
	if c.cfg.TTL > 0 {
		deadline = time.Now().Add(c.cfg.TTL)
	}
	if c.cfg.MaxSize > 0 && len(c.entries) >= c.cfg.MaxSize {
		c.entries = c.entries[1:] // FIFO eviction
	}
	c.entries = append(c.entries, &semanticEntry{
		key:       key.Messages,
		embedding: entry.Embedding,
		entry:     entry,
		deadline:  deadline,
	})
	return nil
}

func (c *SemanticCache) Delete(_ context.Context, key Key) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.entries[:0]
	for _, e := range c.entries {
		if e.key != key.Messages {
			remaining = append(remaining, e)
		}
	}
	c.entries = remaining
	return nil
}

func (c *SemanticCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	return nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

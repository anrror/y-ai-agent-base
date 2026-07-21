package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// entryID produces a unique, prefix-scoped ID for a memory entry.
func (e *Engine) nextEntryID(userID, agentID string) string {
	n := e.seq.Add(1)
	return fmt.Sprintf("mem:%s:%s:%d", userID, agentID, n)
}

// userAgentPrefix returns the ID prefix shared by all entries for a given user
// and agent pair.
func userAgentPrefix(userID, agentID string) string {
	return fmt.Sprintf("mem:%s:%s:", userID, agentID)
}

// Engine orchestrates the three-tier memory system:
//
//  1. Working memory — in-process sliding window of recent messages.
//  2. Short-term memory — persisted store with optional vector similarity.
//  3. Long-term memory — permanent, persisted store.
//
// The Engine is safe for concurrent use.
type Engine struct {
	store      Store
	embedder   provider.EmbeddingProvider
	distiller  *Distiller
	workingMem *WorkingMemory
	seq        atomic.Int64
}

// NewEngine creates an Engine. Pass nil for embedder to disable vector
// similarity search; pass nil for distiller to disable LLM distillation.
func NewEngine(
	store Store,
	cfg types.MemoryConfig,
	embedder provider.EmbeddingProvider,
	distiller *Distiller,
) *Engine {
	ttl := time.Duration(cfg.TTLMillis) * time.Millisecond
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 100
	}

	return &Engine{
		store:      store,
		embedder:   embedder,
		distiller:  distiller,
		workingMem: NewWorkingMemory(maxEntries, ttl),
	}
}

// Save persists an entry to the store and appends it to working memory.
func (e *Engine) Save(ctx context.Context, userID, agentID, content string) error {
	entry := &Entry{
		ID:      e.nextEntryID(userID, agentID),
		Content: content,
	}
	if err := e.store.Add(ctx, entry); err != nil {
		return fmt.Errorf("memory save: %w", err)
	}
	e.workingMem.Add(userID, agentID, content)
	return nil
}

// Search looks up entries in the persisted store by query string. The query
// is matched against entry IDs (prefix) and content (substring).
func (e *Engine) Search(ctx context.Context, query string, limit int) ([]*Entry, error) {
	entries, err := e.store.Search(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("memory search: %w", err)
	}
	return entries, nil
}

// Delete removes an entry from the persisted store by ID.
func (e *Engine) Delete(ctx context.Context, id string) error {
	if err := e.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("memory delete: %w", err)
	}
	return nil
}

// BuildContext retrieves memories relevant to the given user/agent pair and
// formats them as a context string suitable for inclusion in an LLM prompt.
//
// When an embedder is configured the message is embedded and the top matches
// by cosine similarity are returned. Otherwise a simple prefix-based lookup is
// used. Working-memory entries are also included.
//
// Returns an empty string when no memories are found.
func (e *Engine) BuildContext(ctx context.Context, userID, agentID, message string) string {
	prefix := userAgentPrefix(userID, agentID)

	entries, err := e.store.Search(ctx, prefix, 0)
	if err != nil {
		slog.Warn("memory search failed, returning empty context", "error", err)
		return ""
	}

	recent := e.workingMem.GetRecent(userID, agentID, 5)

	if len(entries) == 0 && len(recent) == 0 {
		return ""
	}

	// When an embedder is available, rank store entries by vector similarity.
	if e.embedder != nil && len(entries) > 0 && message != "" {
		entries = e.rankBySimilarity(ctx, message, entries)
	}

	return formatContext(entries, recent)
}

// DistillAndSave extracts a MemoryExtract from the conversation turn and
// persists it to the store. When no distiller is configured the raw
// message+reply pair is saved as-is.
func (e *Engine) DistillAndSave(ctx context.Context, userID, agentID, message, reply string) error {
	extract, err := e.distill(ctx, message, reply)
	if err != nil {
		return fmt.Errorf("distill and save: %w", err)
	}

	content := buildDistilledContent(extract)
	return e.Save(ctx, userID, agentID, content)
}

// Close releases resources held by the store.
func (e *Engine) Close() error {
	if err := e.store.Close(); err != nil {
		return fmt.Errorf("memory close: %w", err)
	}
	return nil
}

// Store returns the underlying Store for direct access.
func (e *Engine) Store() Store {
	return e.store
}

// --- internal helpers ---

func (e *Engine) distill(ctx context.Context, message, reply string) (*MemoryExtract, error) {
	if e.distiller != nil {
		return e.distiller.Distill(ctx, message, reply)
	}
	summary := truncate(message, 200) + " → " + truncate(reply, 200)
	return &MemoryExtract{
		Summary:    summary,
		KeyPoints:  []string{truncate(message, 100)},
		Importance: 0.0,
	}, nil
}

// rankBySimilarity embeds the query, then ranks entries by cosine similarity.
// Returns up to 10 best matches.
func (e *Engine) rankBySimilarity(ctx context.Context, query string, entries []*Entry) []*Entry {
	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		slog.Warn("memory embedding failed, returning unranked entries", "error", err)
		return entries
	}

	type scored struct {
		entry *Entry
		score float64
	}

	items := make([]scored, 0, len(entries))
	for _, entry := range entries {
		vec, err := e.embedder.Embed(ctx, entry.Content)
		if err != nil {
			slog.Debug("memory embedding failed for entry, skipping", "entry_id", entry.ID, "error", err)
			continue
		}
		s := cosineSimilarity(queryVec, vec)
		items = append(items, scored{entry: entry, score: s})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	limit := 10
	if len(items) < limit {
		limit = len(items)
	}

	result := make([]*Entry, limit)
	for i := 0; i < limit; i++ {
		result[i] = items[i].entry
	}
	return result
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func formatContext(entries []*Entry, recent []string) string {
	var b strings.Builder

	if len(entries) > 0 {
		b.WriteString("[Memories]\n")
		for i, entry := range entries {
			fmt.Fprintf(&b, "%d. %s\n", i+1, entry.Content)
		}
		b.WriteString("\n")
	}

	if len(recent) > 0 {
		b.WriteString("[Recent]\n")
		for i, msg := range recent {
			fmt.Fprintf(&b, "%d. %s\n", i+1, msg)
		}
	}

	return strings.TrimSpace(b.String())
}

func buildDistilledContent(extract *MemoryExtract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Importance: %.2f] %s", extract.Importance, extract.Summary)
	if len(extract.KeyPoints) > 0 {
		b.WriteString(" | Key points: ")
		for i, kp := range extract.KeyPoints {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(kp)
		}
	}
	return b.String()
}

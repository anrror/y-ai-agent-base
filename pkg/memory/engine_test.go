package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// --- mockEmbedder ------------------------------------------------------------

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// Produce a deterministic vector from the text hash for repeatable tests.
	vec := make([]float32, m.dim)
	for i := 0; i < m.dim && i < len(text); i++ {
		vec[i] = float32(text[i]) / 255.0
	}
	return vec, nil
}

// --- test helpers -----------------------------------------------------------

func newTestEngine() (*Engine, *InMemoryStore) {
	store := NewInMemoryStore()
	cfg := types.MemoryConfig{
		MaxEntries: 100,
		TTLMillis:  60000,
	}
	engine := NewEngine(store, cfg, nil, nil)
	return engine, store
}

// --- BuildContext -----------------------------------------------------------

func TestEngine_BuildContext_TwoMemories(t *testing.T) {
	engine, store := newTestEngine()
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:001", Content: "User likes Python"}))
	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:002", Content: "User prefers dark mode"}))
	// Extra entry for different agent — should be excluded.
	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:other:003", Content: "Other agent memory"}))

	result := engine.BuildContext(ctx, "u1", "a1", "hello")
	require.NotEmpty(t, result)
	assert.Contains(t, result, "User likes Python")
	assert.Contains(t, result, "User prefers dark mode")
	assert.NotContains(t, result, "Other agent memory")
	assert.Contains(t, result, "[Memories]")
}

func TestEngine_BuildContext_EmptyStore(t *testing.T) {
	engine, _ := newTestEngine()
	ctx := context.Background()

	result := engine.BuildContext(ctx, "u1", "a1", "hello")
	assert.Empty(t, result)
}

func TestEngine_BuildContext_WithWorkingMemory(t *testing.T) {
	engine, store := newTestEngine()
	ctx := context.Background()

	// Only working memory
	engine.workingMem.Add("u1", "a1", "Recent chat about Go")
	engine.workingMem.Add("u1", "a1", "Follow-up on error handling")

	result := engine.BuildContext(ctx, "u1", "a1", "hello")
	assert.Contains(t, result, "[Recent]")
	assert.Contains(t, result, "Recent chat about Go")
	assert.Contains(t, result, "Follow-up on error handling")

	// Combined: store + working memory
	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:001", Content: "User prefers Go"}))
	result = engine.BuildContext(ctx, "u1", "a1", "hello")
	assert.Contains(t, result, "[Memories]")
	assert.Contains(t, result, "[Recent]")
}

// --- Save / Search / Delete -------------------------------------------------

func TestEngine_SaveAndSearch(t *testing.T) {
	engine, _ := newTestEngine()
	ctx := context.Background()

	require.NoError(t, engine.Save(ctx, "u1", "a1", "Memory one"))
	require.NoError(t, engine.Save(ctx, "u1", "a1", "Memory two"))

	entries, err := engine.Search(ctx, userAgentPrefix("u1", "a1"), 0)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// Content search
	entries, err = engine.Search(ctx, "Memory one", 0)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "Memory one", entries[0].Content)
}

func TestEngine_SearchWithLimit(t *testing.T) {
	engine, _ := newTestEngine()
	ctx := context.Background()

	require.NoError(t, engine.Save(ctx, "u1", "a1", "Entry A"))
	require.NoError(t, engine.Save(ctx, "u1", "a1", "Entry B"))
	require.NoError(t, engine.Save(ctx, "u1", "a1", "Entry C"))

	entries, err := engine.Search(ctx, userAgentPrefix("u1", "a1"), 2)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestEngine_Delete(t *testing.T) {
	engine, _ := newTestEngine()
	ctx := context.Background()

	require.NoError(t, engine.Save(ctx, "u1", "a1", "To be deleted"))

	prefix := userAgentPrefix("u1", "a1")
	entries, err := engine.Search(ctx, prefix, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	require.NoError(t, engine.Delete(ctx, entries[0].ID))

	entries, err = engine.Search(ctx, prefix, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// --- DistillAndSave ---------------------------------------------------------

func TestEngine_DistillAndSave_NoLLM(t *testing.T) {
	store := NewInMemoryStore()
	cfg := types.MemoryConfig{MaxEntries: 100, TTLMillis: 60000}
	engine := NewEngine(store, cfg, nil, NewDistiller(nil))
	ctx := context.Background()

	require.NoError(t, engine.DistillAndSave(ctx, "u1", "a1", "What is Go?", "Go is a programming language."))

	entries, err := store.Search(ctx, userAgentPrefix("u1", "a1"), 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Content, "What is Go?")
	assert.Contains(t, entries[0].Content, "Go is a programming language")
}

// --- InMemoryStore ----------------------------------------------------------

func TestInMemoryStore_AddAndSearch(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:001", Content: "Memory A"}))
	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:002", Content: "Memory B"}))

	// Prefix search
	results, err := store.Search(ctx, "mem:u1:a1:", 0)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Content substring (case-insensitive)
	results, err = store.Search(ctx, "memory a", 0)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Memory A", results[0].Content)
}

func TestInMemoryStore_Delete(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:001", Content: "X"}))
	require.NoError(t, store.Delete(ctx, "mem:u1:a1:001"))

	results, err := store.Search(ctx, "mem:u1:a1:", 0)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestInMemoryStore_DeleteNonExistent(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	err := store.Delete(ctx, "nonexistent")
	require.NoError(t, err)
}

func TestInMemoryStore_Len(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	assert.Equal(t, 0, store.Len())
	require.NoError(t, store.Add(ctx, &Entry{ID: "a", Content: "x"}))
	assert.Equal(t, 1, store.Len())
}

func TestInMemoryStore_Close(t *testing.T) {
	store := NewInMemoryStore()
	require.NoError(t, store.Close())
}

// --- WorkingMemory ----------------------------------------------------------

func TestWorkingMemory_AddAndGetRecent(t *testing.T) {
	wm := NewWorkingMemory(10, 0)
	wm.Add("u1", "a1", "Message 1")
	wm.Add("u1", "a1", "Message 2")
	wm.Add("u1", "a1", "Message 3")

	recent := wm.GetRecent("u1", "a1", 0)
	require.Len(t, recent, 3)
	assert.Equal(t, "Message 1", recent[0])
	assert.Equal(t, "Message 3", recent[2])
}

func TestWorkingMemory_GetRecentLimit(t *testing.T) {
	wm := NewWorkingMemory(10, 0)
	wm.Add("u1", "a1", "M1")
	wm.Add("u1", "a1", "M2")
	wm.Add("u1", "a1", "M3")

	recent := wm.GetRecent("u1", "a1", 2)
	require.Len(t, recent, 2)
	// Chronological: newest 2
	assert.Equal(t, "M2", recent[0])
	assert.Equal(t, "M3", recent[1])
}

func TestWorkingMemory_DifferentUsers(t *testing.T) {
	wm := NewWorkingMemory(10, 0)
	wm.Add("u1", "a1", "U1 message")
	wm.Add("u2", "a1", "U2 message")

	assert.Len(t, wm.GetRecent("u1", "a1", 0), 1)
	assert.Len(t, wm.GetRecent("u2", "a1", 0), 1)
}

func TestWorkingMemory_EvictOnOverflow(t *testing.T) {
	wm := NewWorkingMemory(3, 0)
	wm.Add("u1", "a1", "M1")
	wm.Add("u1", "a1", "M2")
	wm.Add("u1", "a1", "M3")
	wm.Add("u1", "a1", "M4") // evicts M1

	recent := wm.GetRecent("u1", "a1", 0)
	require.Len(t, recent, 3)
	assert.Equal(t, "M2", recent[0])
	assert.Equal(t, "M4", recent[2])
}

func TestWorkingMemory_TTLEvict(t *testing.T) {
	fakeClock := clock.NewFakeClock(time.Now())
	wm := NewWorkingMemory(10, 10*time.Millisecond)
	wm.clock = fakeClock
	wm.Add("u1", "a1", "Fresh")

	// Still fresh
	assert.Len(t, wm.GetRecent("u1", "a1", 0), 1)

	fakeClock.Advance(15 * time.Millisecond)

	// Expired
	assert.Empty(t, wm.GetRecent("u1", "a1", 0))
}

// --- Distiller fallback -----------------------------------------------------

func TestDistiller_Fallback(t *testing.T) {
	d := NewDistiller(nil)
	ext, err := d.Distill(context.Background(), "Hello", "Hi there!")
	require.NoError(t, err)
	assert.Equal(t, 0.0, ext.Importance)
	assert.Contains(t, ext.Summary, "Hello")
	assert.Contains(t, ext.Summary, "Hi there")
	assert.NotEmpty(t, ext.KeyPoints)
}

func TestDistiller_ParseValidJSON(t *testing.T) {
	raw := `{"summary":"User asked about Go.","key_points":["Go is compiled","Go has goroutines"],"importance":0.7}`
	ext, err := parseExtract(raw)
	require.NoError(t, err)
	assert.Equal(t, "User asked about Go.", ext.Summary)
	assert.Len(t, ext.KeyPoints, 2)
	assert.Equal(t, 0.7, ext.Importance)
}

func TestDistiller_ParseJSONWithFences(t *testing.T) {
	raw := "```json\n{\"summary\":\"Summary.\\n\",\"key_points\":[\"KP1\"],\"importance\":0.5}\n```"
	ext, err := parseExtract(raw)
	require.NoError(t, err)
	assert.Equal(t, "Summary.\n", ext.Summary)
	assert.Equal(t, 0.5, ext.Importance)
}

func TestDistiller_ParseEmptySummary(t *testing.T) {
	_, err := parseExtract(`{"summary":"","key_points":[],"importance":0}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "summary is empty")
}

// --- formatContext ----------------------------------------------------------

func TestFormatContext_EntriesOnly(t *testing.T) {
	entries := []*Entry{
		{Content: "Memory A"},
		{Content: "Memory B"},
	}
	result := formatContext(entries, nil)
	assert.Contains(t, result, "[Memories]")
	assert.Contains(t, result, "1. Memory A")
	assert.Contains(t, result, "2. Memory B")
	assert.NotContains(t, result, "[Recent]")
}

func TestFormatContext_RecentOnly(t *testing.T) {
	result := formatContext(nil, []string{"Recent 1", "Recent 2"})
	assert.Contains(t, result, "[Recent]")
	assert.Contains(t, result, "1. Recent 1")
	assert.NotContains(t, result, "[Memories]")
}

func TestFormatContext_Both(t *testing.T) {
	entries := []*Entry{{Content: "Memory"}}
	recent := []string{"Recent"}
	result := formatContext(entries, recent)
	assert.True(t, strings.Contains(result, "[Memories]") && strings.Contains(result, "[Recent]"),
		"expected both sections, got: %s", result)
}

// --- cosineSimilarity -------------------------------------------------------

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{1, 2, 3}
	assert.InDelta(t, 1.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	assert.InDelta(t, 0.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
	assert.InDelta(t, 0.0, cosineSimilarity([]float32{1, 2}, []float32{1}), 0.001)
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	assert.InDelta(t, 0.0, cosineSimilarity([]float32{0, 0}, []float32{1, 1}), 0.001)
}

// --- buildDistilledContent --------------------------------------------------

func TestBuildDistilledContent(t *testing.T) {
	ext := &MemoryExtract{
		Summary:    "User prefers Python for scripting.",
		KeyPoints:  []string{"Python", "Scripting"},
		Importance: 0.85,
	}
	result := buildDistilledContent(ext)
	assert.Contains(t, result, "[Importance: 0.85]")
	assert.Contains(t, result, "User prefers Python for scripting.")
	assert.Contains(t, result, "Python; Scripting")
}

// --- BuildContext with mock embedder ----------------------------------------

func TestEngine_BuildContext_WithEmbedder(t *testing.T) {
	store := NewInMemoryStore()
	cfg := types.MemoryConfig{MaxEntries: 100, TTLMillis: 60000}
	embedder := &mockEmbedder{dim: 8}
	engine := NewEngine(store, cfg, embedder, nil)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:001", Content: "User works with Kubernetes"}))
	require.NoError(t, store.Add(ctx, &Entry{ID: "mem:u1:a1:002", Content: "User likes hiking on weekends"}))

	result := engine.BuildContext(ctx, "u1", "a1", "container orchestration")
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "[Memories]")
}

// --- Engine Close -----------------------------------------------------------

func TestEngine_Close(t *testing.T) {
	store := NewInMemoryStore()
	engine := NewEngine(store, types.MemoryConfig{}, nil, nil)
	require.NoError(t, engine.Close())
}

func TestEngine_Store(t *testing.T) {
	store := NewInMemoryStore()
	engine := NewEngine(store, types.MemoryConfig{}, nil, nil)
	assert.Equal(t, store, engine.Store())
}

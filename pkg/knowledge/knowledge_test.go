package knowledge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/component"
	"github.com/anrror/y-ai-agent-base/pkg/knowledge"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// types tests
// ---------------------------------------------------------------------------

func TestSearchOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := &knowledge.SearchParams{}
		// No options applied — should remain zero-valued.
		assert.Equal(t, 0, p.TopK)
		assert.Equal(t, 0.0, p.Threshold)
	})

	t.Run("WithTopK and WithThreshold", func(t *testing.T) {
		p := &knowledge.SearchParams{}
		knowledge.WithTopK(10)(p)
		knowledge.WithThreshold(0.8)(p)
		assert.Equal(t, 10, p.TopK)
		assert.Equal(t, 0.8, p.Threshold)
	})

	t.Run("non-positive values ignored", func(t *testing.T) {
		p := &knowledge.SearchParams{TopK: 5, Threshold: 0.5}
		knowledge.WithTopK(-1)(p)
		knowledge.WithThreshold(0)(p)
		// Should keep existing values.
		assert.Equal(t, 5, p.TopK)
		assert.Equal(t, 0.5, p.Threshold)
	})
}

// ---------------------------------------------------------------------------
// InMemoryStore tests
// ---------------------------------------------------------------------------

func TestInMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := knowledge.NewInMemoryStore()

	t.Run("store and keyword search", func(t *testing.T) {
		err := store.Store(ctx,
			&knowledge.Document{ID: "1", Content: "Go is a compiled programming language"},
			&knowledge.Document{ID: "2", Content: "Python is an interpreted language"},
			&knowledge.Document{ID: "3", Content: "The Rust programming language focuses on safety"},
		)
		require.NoError(t, err)

		results, err := store.Search(ctx, "Go programming")
		require.NoError(t, err)
		require.NotEmpty(t, results)

		// "Go" and "programming" should match doc 1 strongly.
		found := false
		for _, r := range results {
			if r.ID == "1" {
				found = true
				assert.Contains(t, r.Content, "Go")
				assert.Greater(t, r.Score, 0.0)
				break
			}
		}
		assert.True(t, found, "expected doc 1 in search results")

		// Verify descending score order.
		for i := 1; i < len(results); i++ {
			assert.GreaterOrEqual(t, results[i-1].Score, results[i].Score)
		}
	})

	t.Run("search with TopK", func(t *testing.T) {
		results, err := store.Search(ctx, "language", knowledge.WithTopK(2))
		require.NoError(t, err)
		assert.LessOrEqual(t, len(results), 2)
	})

	t.Run("search with threshold", func(t *testing.T) {
		results, err := store.Search(ctx, "xyzzy", knowledge.WithThreshold(0.99))
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("search empty store", func(t *testing.T) {
		emptyStore := knowledge.NewInMemoryStore()
		results, err := emptyStore.Search(ctx, "anything")
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("delete existing", func(t *testing.T) {
		err := store.Delete(ctx, "1")
		require.NoError(t, err)

		results, err := store.Search(ctx, "Go")
		require.NoError(t, err)
		for _, r := range results {
			assert.NotEqual(t, "1", r.ID, "doc 1 should have been deleted")
		}
	})

	t.Run("delete non-existent is no-op", func(t *testing.T) {
		err := store.Delete(ctx, "non-existent")
		assert.NoError(t, err)
	})

	t.Run("close is safe", func(t *testing.T) {
		assert.NoError(t, store.Close())
		assert.NoError(t, store.Close()) // idempotent
	})

	t.Run("store empty ID is skipped", func(t *testing.T) {
		noopStore := knowledge.NewInMemoryStore()
		err := noopStore.Store(ctx, &knowledge.Document{ID: "", Content: "no id"})
		assert.NoError(t, err)

		results, err := noopStore.Search(ctx, "no id")
		assert.NoError(t, err)
		assert.Empty(t, results, "empty ID should be skipped")
	})

	t.Run("upsert replaces existing", func(t *testing.T) {
		upsertStore := knowledge.NewInMemoryStore()
		_ = upsertStore.Store(ctx, &knowledge.Document{ID: "x", Content: "original"})
		_ = upsertStore.Store(ctx, &knowledge.Document{ID: "x", Content: "updated"})

		results, err := upsertStore.Search(ctx, "updated")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "updated", results[0].Content)
	})
}

func TestInMemoryStoreSemanticSearch(t *testing.T) {
	ctx := context.Background()

	// Mock embedding function: case-insensitive "dog" → [1,0], "cat" → [0,1].
	lower := strings.ToLower
	mockEmbed := func(_ context.Context, text string) ([]float32, error) {
		low := lower(text)
		switch {
		case strings.Contains(low, "dog"):
			return []float32{1, 0}, nil
		case strings.Contains(low, "cat"):
			return []float32{0, 1}, nil
		default:
			return []float32{0, 0}, nil
		}
	}

	store := knowledge.NewInMemoryStoreWithEmbedding(mockEmbed)

	t.Run("semantic search with embedFn", func(t *testing.T) {
		err := store.Store(ctx,
			&knowledge.Document{ID: "d1", Content: "Dogs are loyal pets"},
			&knowledge.Document{ID: "d2", Content: "Cats are independent animals"},
		)
		require.NoError(t, err)

		results, err := store.Search(ctx, "dog")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "d1", results[0].ID)
		assert.Greater(t, results[0].Score, 0.0)
	})

	t.Run("embedFn fail returns error", func(t *testing.T) {
		errStore := knowledge.NewInMemoryStoreWithEmbedding(func(_ context.Context, _ string) ([]float32, error) {
			return nil, fmt.Errorf("embedding failed")
		})
		// Store a doc with pre-computed embedding to avoid calling embedFn on Store.
		_ = errStore.Store(ctx, &knowledge.Document{
			ID:        "e1",
			Content:   "test content",
			Embedding: []float32{1, 0},
		})
		// Search calls embedFn for the query — it should fail.
		_, err := errStore.Search(ctx, "test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "embedding failed")
	})
}

func BenchmarkInMemoryStore(b *testing.B) {
	ctx := context.Background()
	store := knowledge.NewInMemoryStore()
	for i := range 100 {
		_ = store.Store(ctx, &knowledge.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: fmt.Sprintf("This is document number %d with some searchable content", i),
		})
	}

	b.ResetTimer()
	for range b.N {
		_, _ = store.Search(ctx, "document searchable content")
	}
}

// ---------------------------------------------------------------------------
// Knowledge component tests
// ---------------------------------------------------------------------------

func TestKnowledgeComponent(t *testing.T) {
	ctx := context.Background()
	store := knowledge.NewInMemoryStore()
	kn := knowledge.New(store, knowledge.DefaultConfig())

	t.Run("ID", func(t *testing.T) {
		assert.Equal(t, "knowledge", kn.ID())
	})

	t.Run("StoreDocs and Search", func(t *testing.T) {
		err := kn.StoreDocs(ctx,
			&knowledge.Document{ID: "k1", Content: "Agent knowledge integration test"},
			&knowledge.Document{ID: "k2", Content: "Another knowledge document about Go agents"},
		)
		require.NoError(t, err)

		results, err := kn.Search(ctx, "knowledge")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "k1", results[0].ID)
	})

	t.Run("DeleteDocs", func(t *testing.T) {
		err := kn.DeleteDocs(ctx, "k2")
		require.NoError(t, err)

		results, err := kn.Search(ctx, "Go")
		require.NoError(t, err)
		for _, r := range results {
			assert.NotEqual(t, "k2", r.ID)
		}
	})

	t.Run("nil store safety", func(t *testing.T) {
		nilKn := knowledge.New(nil, knowledge.DefaultConfig())
		assert.NotNil(t, nilKn)

		results, err := nilKn.Search(ctx, "anything")
		assert.NoError(t, err)
		assert.Nil(t, results)

		err = nilKn.StoreDocs(ctx, &knowledge.Document{ID: "x", Content: "y"})
		assert.Error(t, err)

		assert.NoError(t, nilKn.Close())
	})

	t.Run("Close and idempotent", func(t *testing.T) {
		kn2 := knowledge.New(knowledge.NewInMemoryStore(), knowledge.DefaultConfig())
		assert.NoError(t, kn2.Close())
		assert.NoError(t, kn2.Close())
	})

	t.Run("Init stores pipeline reference", func(t *testing.T) {
		kn3 := knowledge.New(knowledge.NewInMemoryStore(), knowledge.DefaultConfig())
		ic := &component.InitContext{Pipeline: nil}
		assert.NoError(t, kn3.Init(ic))
	})

	t.Run("component interface compliance", func(t *testing.T) {
		var _ component.Component = kn
	})
}

// ---------------------------------------------------------------------------
// Knowledge middleware tests
// ---------------------------------------------------------------------------

func TestKnowledgeMiddleware(t *testing.T) {
	ctx := context.Background()
	store := knowledge.NewInMemoryStore()
	_ = store.Store(ctx,
		&knowledge.Document{ID: "ref1", Content: "The capital of France is Paris."},
		&knowledge.Document{ID: "ref2", Content: "Paris is known for the Eiffel Tower."},
	)

	t.Run("auto-inject adds knowledge to messages", func(t *testing.T) {
		cfg := knowledge.Config{AutoInject: true, TopK: 5}
		kn := knowledge.New(store, cfg)
		_ = kn.Init(&component.InitContext{Pipeline: nil})

		mw := kn.Middleware()
		require.NotNil(t, mw)

		input := &types.ChatInput{
			Messages: []types.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "What is the capital of France?"},
			},
		}
		var output types.ChatOutput

		called := false
		err := mw(func(ctx context.Context, in *types.ChatInput, out *types.ChatOutput) error {
			called = true
			// Verify knowledge was injected.
			require.Len(t, in.Messages, 3, "should have system + knowledge + user")
			assert.Equal(t, "system", in.Messages[0].Role)
			assert.Equal(t, "system", in.Messages[1].Role)
			assert.Equal(t, "user", in.Messages[2].Role)
			assert.Contains(t, in.Messages[1].Content, "knowledge")
			assert.Contains(t, in.Messages[1].Content, "Paris")

			// Verify context contains results.
			results := knowledge.ContextResults(ctx)
			require.NotEmpty(t, results)
			return nil
		})(ctx, input, &output)

		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("no user message - no injection", func(t *testing.T) {
		cfg := knowledge.Config{AutoInject: true, TopK: 5}
		kn := knowledge.New(store, cfg)

		mw := kn.Middleware()
		input := &types.ChatInput{
			Messages: []types.Message{
				{Role: "system", Content: "You are a bot."},
			},
		}
		var output types.ChatOutput

		called := false
		err := mw(func(ctx context.Context, in *types.ChatInput, out *types.ChatOutput) error {
			called = true
			assert.Len(t, in.Messages, 1) // unchanged
			return nil
		})(ctx, input, &output)

		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("auto-inject disabled - pass through", func(t *testing.T) {
		kn := knowledge.New(store, knowledge.DefaultConfig()) // AutoInject=false
		mw := kn.Middleware()

		input := &types.ChatInput{
			Messages: []types.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		var output types.ChatOutput

		called := false
		err := mw(func(ctx context.Context, in *types.ChatInput, out *types.ChatOutput) error {
			called = true
			assert.Len(t, in.Messages, 1) // unchanged
			return nil
		})(ctx, input, &output)

		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("nil store middleware is no-op", func(t *testing.T) {
		kn := knowledge.New(nil, knowledge.DefaultConfig())
		mw := kn.Middleware()
		assert.NotNil(t, mw)
	})
}

// ---------------------------------------------------------------------------
// Agent integration test — pluggability
// ---------------------------------------------------------------------------

func TestAgentKnowledgePluggability(t *testing.T) {
	// Agent WITHOUT knowledge — Knowledge() returns nil.
	t.Run("agent without knowledge returns nil", func(t *testing.T) {
		ag := &agent.Agent{}
		assert.Nil(t, ag.Knowledge())
	})

	t.Run("agent with knowledge returns component", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		kn := knowledge.New(store, knowledge.DefaultConfig())

		ag := &agent.Agent{
			Extensions: map[string]agent.Extension{
				"knowledge": kn,
			},
		}
		got := ag.Knowledge()
		require.NotNil(t, got)
		assert.Equal(t, kn, got)

		// Can search through the agent.
		ctx := context.Background()
		_ = got.StoreDocs(ctx, &knowledge.Document{ID: "a1", Content: "Agent A's private knowledge"})

		results, err := got.Search(ctx, "private knowledge")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "a1", results[0].ID)
	})
}

// ---------------------------------------------------------------------------
// Retriever tests
// ---------------------------------------------------------------------------

func TestStoreRetriever(t *testing.T) {
	ctx := context.Background()
	store := knowledge.NewInMemoryStore()
	_ = store.Store(ctx, &knowledge.Document{ID: "r1", Content: "Retriever test document"})

	t.Run("wraps store", func(t *testing.T) {
		r := knowledge.NewStoreRetriever("test", store)
		assert.Equal(t, "test", r.ID())

		results, err := r.Retrieve(ctx, "Retriever test")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "r1", results[0].ID)
	})

	t.Run("nil store returns empty", func(t *testing.T) {
		r := knowledge.NewStoreRetriever("nil", nil)
		results, err := r.Retrieve(ctx, "anything")
		assert.NoError(t, err)
		assert.Nil(t, results)
	})

	t.Run("default ID", func(t *testing.T) {
		r := knowledge.NewStoreRetriever("", store)
		assert.Equal(t, "store", r.ID())
	})
}

func TestWebSearchRetriever(t *testing.T) {
	ctx := context.Background()

	t.Run("custom search function", func(t *testing.T) {
		r := knowledge.NewWebSearchRetriever(func(_ context.Context, query string) ([]*knowledge.Result, error) {
			return []*knowledge.Result{
				{
					Document: knowledge.Document{
						ID:      "https://example.com/result1",
						Content: "Result about " + query,
						Metadata: map[string]any{
							"title":  "Example Result",
							"url":    "https://example.com/result1",
							"source": "web",
						},
					},
					Score: 0.95,
				},
			}, nil
		})

		assert.Equal(t, "web_search", r.ID())
		results, err := r.Retrieve(ctx, "golang")
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Content, "golang")
		assert.Equal(t, 0.95, results[0].Score)
		assert.Equal(t, "web", results[0].Metadata["source"])
	})

	t.Run("nil search function", func(t *testing.T) {
		r := knowledge.NewWebSearchRetriever(nil)
		results, err := r.Retrieve(ctx, "anything")
		assert.NoError(t, err)
		assert.Nil(t, results)
	})

	t.Run("applies search options", func(t *testing.T) {
		r := knowledge.NewWebSearchRetriever(func(_ context.Context, query string) ([]*knowledge.Result, error) {
			return []*knowledge.Result{
				{Document: knowledge.Document{ID: "1", Content: "low score"}, Score: 0.3},
				{Document: knowledge.Document{ID: "2", Content: "high score"}, Score: 0.9},
			}, nil
		})
		results, err := r.Retrieve(ctx, "test", knowledge.WithThreshold(0.5))
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "2", results[0].ID)
	})
}

func TestWebFetchRetriever(t *testing.T) {
	ctx := context.Background()

	t.Run("custom fetch function", func(t *testing.T) {
		r := knowledge.NewWebFetchRetriever(func(_ context.Context, url string) (string, error) {
			return "Content of " + url, nil
		})

		assert.Equal(t, "web_fetch", r.ID())
		results, err := r.Retrieve(ctx, "https://example.com")
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Content, "https://example.com")
		assert.Equal(t, "https://example.com", results[0].ID)
		assert.Equal(t, 1.0, results[0].Score)
	})

	t.Run("FetchURL convenience", func(t *testing.T) {
		r := knowledge.NewWebFetchRetriever(func(_ context.Context, url string) (string, error) {
			return "fetched: " + url, nil
		})
		result, err := r.FetchURL(ctx, "https://example.org")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.Content, "example.org")
	})

	t.Run("nil fetch function", func(t *testing.T) {
		r := knowledge.NewWebFetchRetriever(nil)
		results, err := r.Retrieve(ctx, "anything")
		assert.NoError(t, err)
		assert.Nil(t, results)

		result, err := r.FetchURL(ctx, "http://example.com")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("empty query", func(t *testing.T) {
		r := knowledge.NewWebFetchRetriever(func(_ context.Context, _ string) (string, error) {
			return "content", nil
		})
		results, err := r.Retrieve(ctx, "")
		assert.NoError(t, err)
		assert.Nil(t, results)
	})
}

// ---------------------------------------------------------------------------
// HybridRetriever tests
// ---------------------------------------------------------------------------

func TestHybridRetriever(t *testing.T) {
	ctx := context.Background()

	makeRetriever := func(id string, docs ...*knowledge.Document) knowledge.Retriever {
		store := knowledge.NewInMemoryStore()
		_ = store.Store(ctx, docs...)
		return knowledge.NewStoreRetriever(id, store)
	}

	t.Run("single retriever", func(t *testing.T) {
		h := knowledge.NewHybridRetriever("h",
			makeRetriever("a", &knowledge.Document{ID: "1", Content: "hello world"}),
		)
		results, err := h.Retrieve(ctx, "hello")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "1", results[0].ID)
	})

	t.Run("multiple retrievers merge and dedup", func(t *testing.T) {
		h := knowledge.NewHybridRetriever("h",
			makeRetriever("a", &knowledge.Document{ID: "1", Content: "Go language"}, &knowledge.Document{ID: "2", Content: "Rust language"}),
			makeRetriever("b", &knowledge.Document{ID: "1", Content: "Go updated"}, &knowledge.Document{ID: "3", Content: "Zig language"}),
		)

		results, err := h.Retrieve(ctx, "language")
		require.NoError(t, err)
		// Should have 3 unique IDs (1, 2, 3), with ID "1" keeping highest score.
		require.NotEmpty(t, results)

		// Verify descending score sort.
		for i := 1; i < len(results); i++ {
			assert.GreaterOrEqual(t, results[i-1].Score, results[i].Score)
		}
	})

	t.Run("empty retrievers", func(t *testing.T) {
		h := knowledge.NewHybridRetriever("empty")
		results, err := h.Retrieve(ctx, "anything")
		assert.NoError(t, err)
		assert.Nil(t, results)
	})

	t.Run("all retrievers error returns empty", func(t *testing.T) {
		errorRetriever := &errorRetriever{}
		h := knowledge.NewHybridRetriever("h", errorRetriever)
		results, err := h.Retrieve(ctx, "test")
		assert.NoError(t, err) // errors are swallowed
		assert.Empty(t, results)
	})

	t.Run("applies TopK", func(t *testing.T) {
		h := knowledge.NewHybridRetriever("h",
			makeRetriever("a",
				&knowledge.Document{ID: "1", Content: "a b c d"},
				&knowledge.Document{ID: "2", Content: "a b c e"},
				&knowledge.Document{ID: "3", Content: "a b c f"},
			),
		)
		results, err := h.Retrieve(ctx, "a b c", knowledge.WithTopK(2))
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("Add appends retrievers", func(t *testing.T) {
		h := knowledge.NewHybridRetriever("h")
		h.Add(makeRetriever("a", &knowledge.Document{ID: "1", Content: "hello"}))
		h.Add(makeRetriever("b", &knowledge.Document{ID: "2", Content: "world"}))
		results, err := h.Retrieve(ctx, "hello")
		require.NoError(t, err)
		require.NotEmpty(t, results)
	})
}

type errorRetriever struct{}

func (e *errorRetriever) ID() string { return "error" }
func (e *errorRetriever) Retrieve(_ context.Context, _ string, _ ...knowledge.SearchOption) ([]*knowledge.Result, error) {
	return nil, fmt.Errorf("always fails")
}

// ---------------------------------------------------------------------------
// Knowledge multi-retriever tests
// ---------------------------------------------------------------------------

func TestKnowledgeMultiRetriever(t *testing.T) {
	ctx := context.Background()

	t.Run("NewWithRetrievers", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		_ = store.Store(ctx, &knowledge.Document{ID: "m1", Content: "multi retriever doc"})
		sr := knowledge.NewStoreRetriever("docs", store)

		kn := knowledge.NewWithRetrievers(knowledge.DefaultConfig(), sr)
		assert.NotNil(t, kn)
		assert.Equal(t, 1, len(kn.Retrievers()))

		results, err := kn.Search(ctx, "multi retriever")
		require.NoError(t, err)
		require.NotEmpty(t, results)
	})

	t.Run("WithRetrievers adds more", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		kn := knowledge.New(store, knowledge.DefaultConfig())
		assert.Equal(t, 1, len(kn.Retrievers()))

		mockWeb := knowledge.NewWebSearchRetriever(func(_ context.Context, _ string) ([]*knowledge.Result, error) {
			return []*knowledge.Result{{Document: knowledge.Document{ID: "web1", Content: "web result"}, Score: 1.0}}, nil
		})
		kn.WithRetrievers(mockWeb)
		assert.Equal(t, 2, len(kn.Retrievers()))
	})

	t.Run("Search queries all retrievers", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		_ = store.Store(ctx, &knowledge.Document{ID: "local1", Content: "local knowledge"})

		mockWeb := knowledge.NewWebSearchRetriever(func(_ context.Context, _ string) ([]*knowledge.Result, error) {
			return []*knowledge.Result{{Document: knowledge.Document{ID: "web1", Content: "web knowledge"}, Score: 0.9}}, nil
		})

		kn := knowledge.NewWithRetrievers(knowledge.DefaultConfig(),
			knowledge.NewStoreRetriever("docs", store),
			mockWeb,
		)

		results, err := kn.Search(ctx, "knowledge")
		require.NoError(t, err)
		require.NotEmpty(t, results)

		ids := make(map[string]bool)
		for _, r := range results {
			ids[r.ID] = true
		}
		assert.True(t, ids["local1"], "should include local result")
		assert.True(t, ids["web1"], "should include web result")
	})

	t.Run("Tools returns tools based on retrievers", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		mockWeb := knowledge.NewWebSearchRetriever(func(_ context.Context, _ string) ([]*knowledge.Result, error) {
			return nil, nil
		})
		mockFetch := knowledge.NewWebFetchRetriever(func(_ context.Context, _ string) (string, error) {
			return "", nil
		})

		kn := knowledge.NewWithRetrievers(knowledge.DefaultConfig(),
			knowledge.NewStoreRetriever("docs", store),
			mockWeb,
			mockFetch,
		)

		tools := kn.Tools()
		require.Len(t, tools, 3)

		toolNames := make(map[string]bool)
		for _, t := range tools {
			toolNames[t.Name()] = true
		}
		assert.True(t, toolNames["search_web"])
		assert.True(t, toolNames["fetch_url"])
		assert.True(t, toolNames["search_knowledge"])
	})

	t.Run("Tools without web retriever", func(t *testing.T) {
		kn := knowledge.New(knowledge.NewInMemoryStore(), knowledge.DefaultConfig())
		tools := kn.Tools()
		// Only search_knowledge, not search_web or fetch_url.
		require.Len(t, tools, 1)
		assert.Equal(t, "search_knowledge", tools[0].Name())
	})

	t.Run("search_knowledge tool execution", func(t *testing.T) {
		store := knowledge.NewInMemoryStore()
		_ = store.Store(ctx, &knowledge.Document{ID: "t1", Content: "tool test content"})
		kn := knowledge.New(store, knowledge.DefaultConfig())

		var searchTool tool.Tool
		for _, t := range kn.Tools() {
			if t.Name() == "search_knowledge" {
				searchTool = t
				break
			}
		}
		require.NotNil(t, searchTool)

		args, _ := json.Marshal(map[string]string{"query": "tool test"})
		result, err := searchTool.Execute(ctx, args)
		require.NoError(t, err)
		assert.Contains(t, result, "tool test content")
	})

	t.Run("knowledge tools with no retrievers", func(t *testing.T) {
		kn := knowledge.NewWithRetrievers(knowledge.DefaultConfig())
		tools := kn.Tools()
		assert.Len(t, tools, 1) // only search_knowledge
		assert.Equal(t, "search_knowledge", tools[0].Name())
	})
}

// ---------------------------------------------------------------------------
// ToolProvider integration with Builder
// ---------------------------------------------------------------------------

func TestKnowledgeToolProviderIntegration(t *testing.T) {
	prov := &mockLLMProvider{}
	pipe := pipeline.New(prov)

	kn := knowledge.New(knowledge.NewInMemoryStore(), knowledge.DefaultConfig())

	// Verify Knowledge has Tools() method.
	tp, ok := interface{}(kn).(interface{ Tools() []tool.Tool })
	require.True(t, ok, "Knowledge should implement Tools()")
	tools := tp.Tools()
	require.NotEmpty(t, tools)

	// Build an agent with knowledge.
	cfg := agent.Config{
		AgentID: "knowledge-agent",
		LLMConfig: types.ModelConfig{
			Model: "test-model", Provider: "test", Temperature: 0.7,
		},
		Status: agent.StatusReady,
	}
	cfg.FillDefaults()

	ag, err := cfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(pipe).
		WithKnowledge(kn).
		Build()
	require.NoError(t, err)
	require.NotNil(t, ag)

	// Tools should be registered from the Knowledge component automatically.
	toolNames := make(map[string]bool)
	for _, t := range ag.Tools {
		toolNames[t.Name()] = true
	}
	assert.True(t, toolNames["search_knowledge"], "search_knowledge should be auto-registered")
}

// mockLLMProvider is a minimal mock for building an agent in tests.
type mockLLMProvider struct{}

func (m *mockLLMProvider) Chat(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
	return "ok", nil
}
func (m *mockLLMProvider) ChatStream(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
	ch := make(chan types.StreamEvent, 2)
	ch <- types.StreamEvent{Type: "chunk", Content: "ok"}
	ch <- types.StreamEvent{Type: "done", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockLLMProvider) Ping(_ context.Context) error { return nil }

var _ provider.LLMProvider = (*mockLLMProvider)(nil)



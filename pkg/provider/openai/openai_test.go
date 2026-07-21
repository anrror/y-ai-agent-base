package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func Test_OpenAIProvider_Name_returns_openai(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})

	// When
	name := p.Name()

	// Then
	assert.Equal(t, "openai", name)
}

func Test_OpenAIProvider_Models_returns_configured_models(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})

	// When
	models := p.Models()

	// Then
	assert.Len(t, models, 1)
	assert.Equal(t, "gpt-4", models[0])
}

func Test_OpenAIProvider_Chat_returns_response_when_server_ok(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		var req types.ChatCompletionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.False(t, req.Stream)
		assert.Equal(t, "gpt-4", req.Model)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"content": "Hello, world!"}}]
		}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})
	ctx := context.Background()
	messages := []types.Message{{Role: "user", Content: "Hi"}}

	// When
	resp, err := p.Chat(ctx, messages, types.ModelConfig{
		Provider:    "openai",
		Model:       "gpt-4",
		Temperature: 0.7,
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, "Hello, world!", resp)
}

func Test_OpenAIProvider_Chat_returns_error_when_server_sends_non_200(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": {"message": "rate limited"}}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})
	ctx := context.Background()
	messages := []types.Message{{Role: "user", Content: "Hi"}}

	// When
	_, err := p.Chat(ctx, messages, types.ModelConfig{Model: "gpt-4"})

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 429")
}

func Test_OpenAIProvider_ChatStream_emits_events_when_server_streams(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/v1/chat/completions"))

		var req types.ChatCompletionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.True(t, req.Stream)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok, "response writer must support flushing")

		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":", "}}]}`,
			`data: {"choices":[{"delta":{"content":"world!"}}]}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "%s\n\n", chunk)
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})
	ctx := context.Background()
	messages := []types.Message{{Role: "user", Content: "Hi"}}

	// When
	events, err := p.ChatStream(ctx, messages, types.ModelConfig{Model: "gpt-4"})
	require.NoError(t, err)

	// Then — collect all events
	var content strings.Builder
	for ev := range events {
		if ev.Error != nil {
			t.Fatalf("unexpected stream error: %v", ev.Error)
		}
		if ev.Content != "" {
			content.WriteString(ev.Content)
		}
	}
	assert.Equal(t, "Hello, world!", content.String())
}

func Test_OpenAIProvider_ChatStream_emits_error_on_bad_SSE(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Malformed JSON in SSE
		_, _ = fmt.Fprintf(w, "data: {{broken\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})
	ctx := context.Background()
	messages := []types.Message{{Role: "user", Content: "Hi"}}

	// When
	events, err := p.ChatStream(ctx, messages, types.ModelConfig{Model: "gpt-4"})
	require.NoError(t, err)

	// Then
	var gotError bool
	for ev := range events {
		if ev.Error != nil {
			gotError = true
		}
	}
	assert.True(t, gotError, "expected an error event for malformed SSE")
}

func Test_OpenAIProvider_ChatStream_cancels_on_ctx_done(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part1\"}}]}\n\n")
		flusher.Flush()
		// Never send [DONE] — will be cancelled
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-4",
	})
	ctx, cancel := context.WithCancel(context.Background())
	messages := []types.Message{{Role: "user", Content: "Hi"}}

	// When
	events, err := p.ChatStream(ctx, messages, types.ModelConfig{Model: "gpt-4"})
	require.NoError(t, err)

	// Read first event
	ev := <-events
	assert.Equal(t, "part1", ev.Content)

	// Then cancel
	cancel()

	// Read remaining — channel should close cleanly
	for range events {
	}
	// No panic = pass
}

func Test_OpenAIProvider_Embed_returns_vector_when_server_ok(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Hello", body.Input)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{"embedding": [0.1, 0.2, 0.3]}]
		}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "text-embed-v3",
	})
	ctx := context.Background()

	// When
	vec, err := p.Embed(ctx, "Hello")

	// Then
	require.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, vec)
}

func Test_OpenAIProvider_Embed_returns_error_when_no_data(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "text-embed-v3",
	})
	ctx := context.Background()

	// When
	_, err := p.Embed(ctx, "Hello")

	// Then
	require.Error(t, err)
}

func Test_OpenAIProvider_Check_returns_allowed_when_server_oks(t *testing.T) {
	// Given
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "/v1/moderations", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var body struct {
			Input string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "safe text", body.Input)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [{"flagged": false}]
		}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "mod-latest",
	})
	ctx := context.Background()

	// When
	allowed, err := p.Check(ctx, "safe text")

	// Then
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.True(t, called)
}

func Test_OpenAIProvider_Check_returns_not_allowed_when_flagged(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [{"flagged": true, "categories": {"hate": true}}]
		}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "mod-latest",
	})
	ctx := context.Background()

	// When
	allowed, err := p.Check(ctx, "dangerous text")

	// Then
	require.NoError(t, err)
	assert.False(t, allowed)
}

func Test_OpenAIProvider_Check_returns_error_when_no_results(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": []}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "mod-latest",
	})
	ctx := context.Background()

	// When
	_, err := p.Check(ctx, "text")

	// Then
	require.Error(t, err)
}

func Test_NewOpenAIProvider_normalizes_baseURL(t *testing.T) {
	// Given — a server that records which path was called
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	tests := []struct {
		name     string
		baseURL  string
		wantPath string
	}{
		{"removes trailing slash", srv.URL + "/", "/v1/chat/completions"},
		{"keeps bare URL", srv.URL, "/v1/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastPath = ""
			p := NewOpenAIProvider(&provider.ProviderConfig{
				Type:    "openai",
				APIKey:  "sk-test",
				BaseURL: tt.baseURL,
				Model:   "gpt-4",
			})
			ctx := context.Background()
			_, err := p.Chat(ctx, []types.Message{{Role: "user", Content: "Hi"}}, types.ModelConfig{Model: "gpt-4"})
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, lastPath)
		})
	}
}

func Test_OpenAIProvider_Close_is_noop(t *testing.T) {
	// Given
	p := NewOpenAIProvider(&provider.ProviderConfig{
		Type:    "openai",
		APIKey:  "sk-test",
		BaseURL: "https://api.example.com",
		Model:   "gpt-4",
	})

	// When
	err := p.Close()

	// Then
	assert.NoError(t, err)
}

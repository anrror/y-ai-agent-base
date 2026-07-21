package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/internal/handler"
	"github.com/anrror/y-ai-agent-base/internal/middleware"
	"github.com/anrror/y-ai-agent-base/internal/router"
	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/config"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/store"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

const testJWTSecret = "test-secret-key-32-chars-minimum"

// mockProvider implements provider.LLMProvider for testing.
type mockProvider struct {
	chatText     string
	streamEvents []types.StreamEvent
}

func (m *mockProvider) Chat(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
	return m.chatText, nil
}

func (m *mockProvider) ChatStream(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
	ch := make(chan types.StreamEvent)
	go func() {
		defer close(ch)
		for _, evt := range m.streamEvents {
			ch <- evt
		}
	}()
	return ch, nil
}

func (m *mockProvider) Ping(_ context.Context) error { return nil }

var _ provider.LLMProvider = (*mockProvider)(nil)

func newTestServer(t *testing.T) (*gin.Engine, *mockProvider, *agent.Registry, func()) {
	t.Helper()

	// Set required env vars for config.Load validation.
	t.Setenv("YAI_SERVER_PORT", "8080")
	t.Setenv("YAI_PROVIDERS_CHAT_API_KEY", "test-api-key")
	t.Setenv("YAI_PROVIDERS_CHAT_MODEL", "gpt-4o")
	t.Setenv("YAI_PROVIDERS_BASE_URL", "https://api.openai.com/v1")
	t.Setenv("YAI_AUTH_JWT_SECRET", testJWTSecret)

	prov := &mockProvider{
		chatText: "Hello, I am a test agent!",
		streamEvents: []types.StreamEvent{
			{Content: "Hello"},
			{Content: ", "},
			{Content: "world"},
			{Type: "done", Done: true},
		},
	}

	cfg, err := config.Load()
	require.NoError(t, err, "config.Load should succeed with defaults")

	pipe := pipeline.New(prov, pipeline.MetricsMiddleware(pipeline.NewMetrics()))
	agentStore := store.NewMemoryStore()
	registry := agent.NewRegistry()

	agCfg := agent.Config{
		AgentID: "assistant",
		LLMConfig: types.ModelConfig{
			Model:       "gpt-4o",
			Temperature: 0.7,
			MaxTokens:   1024,
		},
		Identity: &agent.Identity{
			Name:        "Assistant",
			Role:        "helper",
			Description: "Test assistant",
		},
		PromptTmpl: "You are a helpful assistant.",
		Status:     agent.StatusReady,
	}
	agCfg.FillDefaults()

	testAgent, err := agCfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(pipe).
		Build()
	require.NoError(t, err)

	err = registry.Register(testAgent)
	require.NoError(t, err)

	telemetry := middleware.NewTelemetryHook(nil)
	mw := middleware.New(testJWTSecret, telemetry)
	mw.RateLimitCfg.Enabled = false

	h := handler.New(registry, agentStore, cfg, &provider.ProviderSet{Chat: prov}, pipe, pipeline.NewMetrics())
	r := router.Setup(h, mw)

	cleanup := func() {
		_ = agentStore.Close()
		for _, ag := range registry.List() {
			_ = ag.Close()
		}
	}

	return r, prov, registry, cleanup
}

func authHeader() string {
	return "Bearer " + signJWT(testJWTSecret, "test-user", "")
}

// signJWT creates a HS256 JWT with the given secret, subject, and optional agent_id.
// The token expires in 1 hour.
func signJWT(secret, sub, agentID string) string {
	header := `{"alg":"HS256","typ":"JWT"}`
	payload := fmt.Sprintf(`{"sub":%q,"exp":%d`, sub, time.Now().Add(time.Hour).Unix())
	if agentID != "" {
		payload += fmt.Sprintf(`,"agent_id":%q`, agentID)
	}
	payload += "}"

	enc := func(raw string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(raw))
	}

	headerB64 := enc(header)
	payloadB64 := enc(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(headerB64 + "." + payloadB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return headerB64 + "." + payloadB64 + "." + sig
}

// ── Health check ──────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.NotEmpty(t, body["timestamp"])
}

// ── Agent CRUD ────────────────────────────────────────────

func TestRegisterAgent(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"agent_id":    "test-bot",
		"model":       "gpt-4o",
		"temperature": 0.5,
		"max_tokens":  512,
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/agents", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "test-bot", body["agent_id"])
	assert.Equal(t, "ready", body["status"])
}

func TestRegisterAgentDuplicate(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"agent_id": "assistant",
		"model":    "gpt-4o",
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/agents", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestListAgents(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	agents, ok := body["agents"].([]any)
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(agents), 1)
}

func TestGetAgent(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/agents/assistant", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "assistant", body["agent_id"])
}

func TestGetAgentNotFound(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/agents/nonexistent", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteAgent(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/agents/assistant", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteAgentNotFound(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/agents/missing", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Chat completions (JSON) ───────────────────────────────

func TestChatCompletionsJSON(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"model": "assistant",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello!"},
		},
		"stream": false,
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "chat.completion", body["object"])
}

func TestChatCompletionsMissingModel(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "Hi"},
		},
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestChatCompletionsAgentNotFound(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"model": "nonexistent",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Chat completions (SSE stream) ─────────────────────────

func TestChatCompletionsStream(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	payload := map[string]any{
		"model": "assistant",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello!"},
		},
		"stream": true,
	}
	b, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")

	body := w.Body.String()
	assert.Contains(t, body, "data: ")
	assert.Contains(t, body, "[DONE]")
}

// ── Auth ──────────────────────────────────────────────────

func TestAuthMissingHeader(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/agents", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthInvalidToken(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── Rate limit ────────────────────────────────────────────

func TestRateLimit(t *testing.T) {
	_, prov, reg, cleanup := newTestServer(t)
	defer cleanup()

	t.Setenv("YAI_SERVER_PORT", "8080")
	t.Setenv("YAI_LLM_API_KEY", "test-api-key")
	t.Setenv("YAI_AUTH_JWT_SECRET", testJWTSecret)

	cfg, err := config.Load()
	require.NoError(t, err)

	pipe := pipeline.New(prov, pipeline.MetricsMiddleware(pipeline.NewMetrics()))
	agStore := store.NewMemoryStore()

	telemetry := middleware.NewTelemetryHook(nil)
	mw := middleware.New(testJWTSecret, telemetry)
	mw.RateLimitCfg.Enabled = true
	mw.RateLimitCfg.RequestsPerMin = 3
	mw.RateLimitCfg.Burst = 1

	h := handler.New(reg, agStore, cfg, &provider.ProviderSet{Chat: prov}, pipe, pipeline.NewMetrics())
	r2 := router.Setup(h, mw)

	// 1st request passes (burst).
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	r2.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 2nd request rate-limited.
	w = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	r2.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

// ── Mock provider ─────────────────────────────────────────

func TestMockProviderChat(t *testing.T) {
	mp := &mockProvider{chatText: "test response"}
	result, err := mp.Chat(context.Background(), nil, types.ModelConfig{})
	require.NoError(t, err)
	assert.Equal(t, "test response", result)
}

func TestMockProviderChatStream(t *testing.T) {
	mp := &mockProvider{
		streamEvents: []types.StreamEvent{
			{Content: "Hello"},
			{Type: "done", Done: true},
		},
	}
	ch, err := mp.ChatStream(context.Background(), nil, types.ModelConfig{})
	require.NoError(t, err)

	var received []string
	for evt := range ch {
		if evt.Content != "" {
			received = append(received, evt.Content)
		}
	}
	assert.Equal(t, []string{"Hello"}, received)
}

// ── Server integration ────────────────────────────────────

func TestServerStartupAndHealth(t *testing.T) {
	r, _, _, cleanup := newTestServer(t)
	defer cleanup()

	ts := httptest.NewServer(r)
	defer ts.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/health", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

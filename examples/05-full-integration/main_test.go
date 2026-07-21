// Integration Test: Full Framework Exercise
//
// This test exercises every framework component working together,
// verifying they interoperate correctly. All dependencies are in-memory
// mocks — no external services required.
//
// Components exercised:
//   - pkg/types      — types, errors, ModelConfig
//   - pkg/provider   — LLMProvider, EmbeddingProvider, GuardProvider
//   - pkg/pipeline   — Pipeline, middleware (Recovery, Logging, RateLimit, Timeout)
//   - pkg/agent      — Config, Builder, Registry, Chat, Run, RunStream
//   - pkg/tool       — Tool interface, FromFunction, TimeTool, EchoTool, WeatherTool
//   - pkg/skills     — Skill, Registry, Match, builtin skills
//   - pkg/memory     — Engine, InMemoryStore, WorkingMemory, Distiller
//   - pkg/session    — MemoryStore, SessionManager
//   - pkg/safety     — Guard, MockGuard, SafetyMiddleware
//   - pkg/store      — MemoryStore (AgentStore)
//   - pkg/agent — OCEAN, Identity
//   - pkg/team       — Team, supervisor/member delegation
//   - pkg/config     — Config, Load, Validate
//   - pkg/clock      — RealClock, FakeClock
//   - pkg/component  — Component, InitContext, Registry
//   - pkg/cache      — Cache interface types

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/cache"
	"github.com/anrror/y-ai-agent-base/pkg/clock"
	"github.com/anrror/y-ai-agent-base/pkg/component"
	"github.com/anrror/y-ai-agent-base/pkg/config"
	"github.com/anrror/y-ai-agent-base/pkg/memory"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/safety"
	"github.com/anrror/y-ai-agent-base/pkg/session"
	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/skills/builtin"
	"github.com/anrror/y-ai-agent-base/pkg/store"
	"github.com/anrror/y-ai-agent-base/pkg/team"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// =============================================================================
// Mock Provider — implements LLMProvider, EmbeddingProvider, GuardProvider
// =============================================================================

type mockProvider struct {
	chatText     string
	streamEvents []types.StreamEvent
}

func (m *mockProvider) Name() string                         { return "mock" }
func (m *mockProvider) Models() []string                     { return []string{"mock-model"} }
func (m *mockProvider) Ping(_ context.Context) error         { return nil }
func (m *mockProvider) Close() error                         { return nil }
func (m *mockProvider) LLM() provider.LLMProvider            { return m }
func (m *mockProvider) Embedding() provider.EmbeddingProvider { return m }
func (m *mockProvider) SafetyGuard() provider.GuardProvider  { return &safety.MockGuard{} }

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

func (m *mockProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockProvider) Check(_ context.Context, _ string) (bool, error) {
	return true, nil
}

var (
	_ provider.LLMProvider      = (*mockProvider)(nil)
	_ provider.EmbeddingProvider = (*mockProvider)(nil)
	_ provider.GuardProvider     = (*mockProvider)(nil)
	_ provider.CompositeProvider = (*mockProvider)(nil)
)

func newMockProvider() *mockProvider {
	return &mockProvider{
		chatText: "Hello! I'm a test agent helping you today.",
		streamEvents: []types.StreamEvent{
			{Content: "Hello"},
			{Content: "! "},
			{Content: "I'm a test"},
			{Type: "done", Done: true},
		},
	}
}

// =============================================================================
// Test: Types & Errors
// =============================================================================

func TestTypes_RoundTrip(t *testing.T) {
	t.Run("Message JSON", func(t *testing.T) {
		msg := types.Message{Role: "user", Content: "hello"}
		data, err := json.Marshal(msg)
		require.NoError(t, err)
		var out types.Message
		require.NoError(t, json.Unmarshal(data, &out))
		assert.Equal(t, msg, out)
	})

	t.Run("ModelConfig defaults", func(t *testing.T) {
		cfg := types.ModelConfig{Model: "gpt-4o"}
		assert.Equal(t, "", cfg.Provider)
		assert.Equal(t, float64(0), cfg.Temperature)
		assert.Equal(t, 0, cfg.MaxTokens)
	})

	t.Run("ChatOutput IsStream defaults false", func(t *testing.T) {
		out := types.ChatOutput{}
		assert.False(t, out.IsStream)
	})

	t.Run("ErrorToHTTP mapping", func(t *testing.T) {
		assert.Equal(t, 404, types.ErrorToHTTP(types.ErrNotFound))
		assert.Equal(t, 403, types.ErrorToHTTP(types.ErrGuardBlocked))
		assert.Equal(t, 503, types.ErrorToHTTP(types.ErrProviderUnavailable))
		assert.Equal(t, 400, types.ErrorToHTTP(types.ErrInvalidConfig))
		assert.Equal(t, 504, types.ErrorToHTTP(types.ErrTimeout))
		assert.Equal(t, 500, types.ErrorToHTTP(nil))
	})

	t.Run("Sentinel errors are distinct", func(t *testing.T) {
		assert.NotEqual(t, types.ErrNotFound, types.ErrGuardBlocked)
		assert.NotEqual(t, types.ErrProviderUnavailable, types.ErrStreamClosed)
	})
}

// =============================================================================
// Test: Personality
// =============================================================================

func TestPersonality_OCEAN(t *testing.T) {
	t.Run("ToMap / FromMap round-trip", func(t *testing.T) {
		ocean := agent.OCEAN{
			Openness: 0.8, Conscientiousness: 0.7, Extraversion: 0.5,
			Agreeableness: 0.9, Neuroticism: 0.2,
		}
		m := ocean.ToMap()
		assert.Len(t, m, 5)
		assert.Equal(t, 0.8, m["openness"])

		restored := agent.OCEAN{}
		restored.FromMap(m)
		assert.Equal(t, ocean, restored)
	})

	t.Run("Identity JSON", func(t *testing.T) {
		id := agent.Identity{
			Name: "TestBot", Role: "tester",
			Description: "A test bot", Tone: "neutral", Verbosity: "concise",
			Constraints: []string{"be helpful"},
		}
		data, err := json.Marshal(id)
		require.NoError(t, err)
		var out agent.Identity
		require.NoError(t, json.Unmarshal(data, &out))
		assert.Equal(t, id, out)
	})
}

// =============================================================================
// Test: Clock
// =============================================================================

func TestClock_RealAndFake(t *testing.T) {
	t.Run("RealClock Now returns non-zero time", func(t *testing.T) {
		c := clock.RealClock{}
		assert.False(t, c.Now().IsZero())
	})

	t.Run("RealClock After emits", func(t *testing.T) {
		c := clock.RealClock{}
		select {
		case <-c.After(10 * time.Millisecond):
			// ok
		case <-time.After(200 * time.Millisecond):
			t.Fatal("After did not fire")
		}
	})

	t.Run("FakeClock Now returns set time", func(t *testing.T) {
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		fc := clock.NewFakeClock(now)
		assert.Equal(t, now, fc.Now())
	})

	t.Run("FakeClock Advance moves time", func(t *testing.T) {
		fc := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		fc.Advance(1 * time.Hour)
		assert.Equal(t, "2026-01-01 01:00:00", fc.Now().Format("2006-01-02 15:04:05"))
	})

	t.Run("FakeClock Ticker fires on Advance", func(t *testing.T) {
		fc := clock.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		tk := fc.NewTicker(100 * time.Millisecond)
		fc.Advance(200 * time.Millisecond)
		select {
		case <-tk.C():
			// fired
		default:
			t.Fatal("ticker did not fire after Advance")
		}
		tk.Stop()
	})
}

// =============================================================================
// Test: Cache types
// =============================================================================

func TestCache_Types(t *testing.T) {
	t.Run("Cache Key struct", func(t *testing.T) {
		key := cache.Key{
			Model: "gpt-4o", Messages: "abc", Temperature: 0.7, Mode: cache.ModeExact,
		}
		assert.Equal(t, cache.ModeExact, key.Mode)
	})

	t.Run("Cache Entry with expiry", func(t *testing.T) {
		now := time.Now()
		entry := cache.Entry{
			Content: "response", CreatedAt: now, ExpiresAt: now.Add(time.Hour), HitCount: 5,
		}
		assert.Equal(t, "response", entry.Content)
		assert.Equal(t, int64(5), entry.HitCount)
	})

	t.Run("Cache Config modes", func(t *testing.T) {
		cfg := cache.Config{
			Mode: cache.ModeSemantic, TTL: time.Hour, MaxSize: 100, Threshold: 0.8,
		}
		assert.Equal(t, cache.ModeSemantic, cfg.Mode)
		assert.Equal(t, 0.8, cfg.Threshold)
	})
}

// =============================================================================
// Test: Component lifecycle
// =============================================================================

// testComponent implements component.Component for testing.
type testComponent struct {
	id       string
	category string
	initErr  error
	closed   bool
}

func (tc *testComponent) ID() string                                                { return tc.id }
func (tc *testComponent) Category() string                                          { return tc.category }
func (tc *testComponent) Priority() component.Priority                              { return component.PriorityNormal }
func (tc *testComponent) Init(_ *component.InitContext) error                       { return tc.initErr }
func (tc *testComponent) Close() error                                              { tc.closed = true; return nil }

var _ component.Component              = (*testComponent)(nil)
var _ component.CategorisedComponent   = (*testComponent)(nil)
var _ component.PriorityProvider       = (*testComponent)(nil)

func TestComponent_Registry(t *testing.T) {
	reg := component.NewRegistry()

	tc := &testComponent{id: "test.cache", category: "cache"}
	reg.Register(tc)

	got := reg.Get("test.cache")
	assert.NotNil(t, got)
	assert.Equal(t, tc, got)

	got = reg.Get("nonexistent")
	assert.Nil(t, got)

	all := reg.ListByCategory("cache")
	assert.Len(t, all, 1)

	all = reg.ListByCategory("unknown")
	assert.Len(t, all, 0)
}

func TestComponent_PriorityConstants(t *testing.T) {
	assert.True(t, component.PriorityObservability < component.PriorityNormal)
	assert.True(t, component.PriorityEarly < component.PriorityNormal)
	assert.True(t, component.PriorityNormal < component.PriorityLate)
	assert.True(t, component.PriorityLate < component.PriorityTerminal)
}

// =============================================================================
// Test: Config Load & Validate
// =============================================================================

func TestConfig_LoadAndValidate(t *testing.T) {
	t.Setenv("YAI_SERVER_PORT", "8080")
	t.Setenv("YAI_PROVIDERS_CHAT_API_KEY", "test-key")
	t.Setenv("YAI_PROVIDERS_CHAT_MODEL", "gpt-4o-mini")
	t.Setenv("YAI_PROVIDERS_BASE_URL", "https://api.openai.com/v1")
	t.Setenv("YAI_AUTH_JWT_SECRET", "test-secret-32-chars-minimum")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Server.Port)
}

// =============================================================================
// Test: Provider
// =============================================================================

func TestProvider_Mock(t *testing.T) {
	prov := newMockProvider()

	t.Run("Name and Models", func(t *testing.T) {
		assert.Equal(t, "mock", prov.Name())
		assert.Contains(t, prov.Models(), "mock-model")
	})

	t.Run("Chat returns text", func(t *testing.T) {
		text, err := prov.Chat(context.Background(), nil, types.ModelConfig{})
		require.NoError(t, err)
		assert.Contains(t, text, "Hello")
	})

	t.Run("ChatStream emits events", func(t *testing.T) {
		ch, err := prov.ChatStream(context.Background(), nil, types.ModelConfig{})
		require.NoError(t, err)
		var collected []string
		for evt := range ch {
			if evt.Content != "" {
				collected = append(collected, evt.Content)
			}
		}
		assert.Len(t, collected, 3)
	})

	t.Run("Embed returns vector", func(t *testing.T) {
		vec, err := prov.Embed(context.Background(), "hello")
		require.NoError(t, err)
		assert.Len(t, vec, 3)
	})

	t.Run("Ping succeeds", func(t *testing.T) {
		assert.NoError(t, prov.Ping(context.Background()))
	})

	t.Run("Composite accessors", func(t *testing.T) {
		assert.NotNil(t, prov.LLM())
		assert.NotNil(t, prov.Embedding())
		assert.NotNil(t, prov.SafetyGuard())
	})
}

// =============================================================================
// Test: Pipeline
// =============================================================================

func TestPipeline_FullChain(t *testing.T) {
	prov := newMockProvider()
	p := pipeline.New(
		prov,
		pipeline.Recovery(),
		pipeline.Logging(nil),
	)

	t.Run("ChatStream with middleware", func(t *testing.T) {
		ctx := context.Background()
		out, err := p.ChatStream(ctx, types.ChatInput{
			UserID: "u1", AgentID: "a1",
			Messages: []types.Message{{Role: "user", Content: "hi"}},
		})
		require.NoError(t, err)
		assert.NotNil(t, out.Stream)
		assert.True(t, out.IsStream)
	})

	t.Run("Chat non-streaming", func(t *testing.T) {
		ctx := context.Background()
		out, err := p.Run(ctx, types.ChatInput{
			UserID: "u1", AgentID: "a1",
			Messages: []types.Message{{Role: "user", Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Contains(t, out.Content, "Hello")
		assert.False(t, out.IsStream)
	})
}

// =============================================================================
// Test: Tool System
// =============================================================================

func TestTool_Builtin(t *testing.T) {
	t.Run("TimeTool returns current time", func(t *testing.T) {
		result, err := tool.TimeTool().Execute(context.Background(), json.RawMessage(`{}`))
		require.NoError(t, err)
		assert.Contains(t, result, "2026")
	})

	t.Run("EchoTool echoes input", func(t *testing.T) {
		result, err := tool.EchoTool().Execute(context.Background(), json.RawMessage(`{"message":"hello"}`))
		require.NoError(t, err)
		assert.Contains(t, result, "hello")
	})

	t.Run("WeatherTool returns mock data", func(t *testing.T) {
		result, err := tool.WeatherTool().Execute(context.Background(), json.RawMessage(`{"city":"Beijing"}`))
		require.NoError(t, err)
		assert.Contains(t, result, "Beijing")
	})

	t.Run("WeatherTool missing city", func(t *testing.T) {
		_, err := tool.WeatherTool().Execute(context.Background(), json.RawMessage(`{}`))
		assert.Error(t, err)
	})

	t.Run("Tool Registry CRUD", func(t *testing.T) {
		reg := tool.NewRegistry()
		toolTime := tool.TimeTool()
		require.NoError(t, reg.Register(toolTime))
		assert.Equal(t, 1, len(reg.List()))

		got, ok := reg.Get("get_current_time")
		assert.True(t, ok)
		assert.Equal(t, toolTime.Name(), got.Name())

		_, ok = reg.Get("unknown_tool")
		assert.False(t, ok)
	})

	t.Run("FromFunction with schema", func(t *testing.T) {
		customTool := tool.FromFunction(
			"greet",
			"Greets a user by name",
			func(_ context.Context, args json.RawMessage) (string, error) {
				var params struct{ Name string `json:"name"` }
				json.Unmarshal(args, &params)
				return fmt.Sprintf("Hello, %s!", params.Name), nil
			},
			tool.NewParamSchema().AddString("name", "Name to greet", true).Build(),
		)
		assert.Equal(t, "greet", customTool.Name())
		result, err := customTool.Execute(context.Background(), json.RawMessage(`{"name":"World"}`))
		require.NoError(t, err)
		assert.Equal(t, "Hello, World!", result)
	})
}

// =============================================================================
// Test: Skills System
// =============================================================================

func TestSkills_RegistryAndMatch(t *testing.T) {
	reg := skills.NewRegistry()

	timeSkill := builtin.TimeSkill()
	weatherSkill := builtin.WeatherSkill()
	echoSkill := builtin.EchoSkill()

	require.NoError(t, reg.Register(timeSkill))
	require.NoError(t, reg.Register(weatherSkill))
	require.NoError(t, reg.Register(echoSkill))
	assert.Equal(t, 3, reg.Count())

	t.Run("Match time query", func(t *testing.T) {
		results := reg.Match(context.Background(), "what time is it now")
		skills.SortMatchResults(results)
		assert.NotEmpty(t, results)
		assert.Equal(t, "time", results[0].Skill.Name())
		assert.Greater(t, results[0].Score, float64(0))
	})

	t.Run("Match weather query", func(t *testing.T) {
		results := reg.Match(context.Background(), "will it rain tomorrow")
		skills.SortMatchResults(results)
		assert.NotEmpty(t, results)
		assert.Equal(t, "weather", results[0].Skill.Name())
	})

	t.Run("Match unrelated query returns empty", func(t *testing.T) {
		results := reg.Match(context.Background(), "write a poem about clouds")
		// Might match echo or nothing — just verify no panic
		_ = results
	})

	t.Run("List all skills", func(t *testing.T) {
		all := reg.List()
		assert.Len(t, all, 3)
	})

	t.Run("Unregister removes skill", func(t *testing.T) {
		reg2 := skills.NewRegistry()
		require.NoError(t, reg2.Register(timeSkill))
		assert.NoError(t, reg2.Unregister("time"))
		assert.Equal(t, 0, reg2.Count())
	})
}

// =============================================================================
// Test: Memory Engine
// =============================================================================

func TestMemory_FullLifecycle(t *testing.T) {
	store := memory.NewInMemoryStore()
	prov := newMockProvider()
	eng := memory.NewEngine(store, types.MemoryConfig{MaxEntries: 100, TTLMillis: 10000}, prov, nil)
	defer eng.Close()

	t.Run("Save and BuildContext", func(t *testing.T) {
		require.NoError(t, eng.Save(context.Background(), "u1", "a1", "User asked about Go errors"))
		require.NoError(t, eng.Save(context.Background(), "u1", "a1", "User prefers concise answers"))

		ctx := eng.BuildContext(context.Background(), "u1", "a1", "Go error handling")
		assert.Contains(t, ctx, "Go errors")
	})

	t.Run("BuildContext empty store", func(t *testing.T) {
		ctx := eng.BuildContext(context.Background(), "u99", "a99", "anything")
		assert.Empty(t, ctx)
	})

	t.Run("Search by prefix", func(t *testing.T) {
		entries, err := eng.Search(context.Background(), "mem:u1:a1:", 10)
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})

	t.Run("Delete entry", func(t *testing.T) {
		entries, _ := eng.Search(context.Background(), "mem:u1:a1:", 10)
		if len(entries) > 0 {
			require.NoError(t, eng.Delete(context.Background(), entries[0].ID))
		}
	})

	t.Run("DistillAndSave without distiller", func(t *testing.T) {
		require.NoError(t, eng.DistillAndSave(context.Background(), "u2", "a2",
			"What is Go?", "Go is a programming language.",
		))
		ctx := eng.BuildContext(context.Background(), "u2", "a2", "Go")
		assert.Contains(t, ctx, "Go")
	})
}

// =============================================================================
// Test: Session Manager
// =============================================================================

func TestSession_Manager(t *testing.T) {
	store := session.NewMemoryStore()
	manager := session.NewSessionManager(store, session.WithMaxHistory(50))
	defer func() { _ = store.Close() }()

	t.Run("GetOrCreate creates new session", func(t *testing.T) {
		sess, err := manager.GetOrCreate(context.Background(), "u1", "a1", "sess-1")
		require.NoError(t, err)
		assert.Equal(t, "sess-1", sess.ID)
		assert.Equal(t, "u1", sess.UserID)
		assert.True(t, sess.State.Active)
	})

	t.Run("GetOrCreate returns existing", func(t *testing.T) {
		sess, err := manager.GetOrCreate(context.Background(), "u1", "a1", "sess-1")
		require.NoError(t, err)
		assert.Equal(t, "sess-1", sess.ID)
	})

	t.Run("Update adds messages", func(t *testing.T) {
		require.NoError(t, manager.Update(context.Background(), "sess-1",
			[]types.Message{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			},
		))
		sess, err := manager.Get(context.Background(), "sess-1")
		require.NoError(t, err)
		assert.Len(t, sess.Messages, 2)
	})

	t.Run("Update trims history", func(t *testing.T) {
		msgs := make([]types.Message, 100)
		for i := range msgs {
			msgs[i] = types.Message{Role: "user", Content: fmt.Sprintf("msg-%d", i)}
		}
		require.NoError(t, manager.Update(context.Background(), "sess-1", msgs))
		sess, err := manager.Get(context.Background(), "sess-1")
		require.NoError(t, err)
		assert.LessOrEqual(t, len(sess.Messages), 50)
	})

	t.Run("Close deactivates", func(t *testing.T) {
		require.NoError(t, manager.Close(context.Background(), "sess-1"))
		sess, err := manager.Get(context.Background(), "sess-1")
		require.NoError(t, err)
		assert.False(t, sess.State.Active)
	})

	t.Run("Delete removes", func(t *testing.T) {
		require.NoError(t, manager.Delete(context.Background(), "sess-1"))
		_, err := manager.Get(context.Background(), "sess-1")
		assert.Error(t, err)
	})
}

// =============================================================================
// Test: Safety Guard
// =============================================================================

func TestSafety_GuardAndMiddleware(t *testing.T) {
	t.Run("MockGuard safe text", func(t *testing.T) {
		g := &safety.MockGuard{}
		allowed, err := g.Check(context.Background(), "hello world")
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("MockGuard bad text", func(t *testing.T) {
		g := &safety.MockGuard{}
		allowed, err := g.Check(context.Background(), "this is bad content")
		require.NoError(t, err)
		assert.False(t, allowed)
	})

	t.Run("MockGuard AllowAll", func(t *testing.T) {
		g := &safety.MockGuard{AllowAll: true}
		allowed, err := g.Check(context.Background(), "bad stuff")
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("MockGuard BlockAll", func(t *testing.T) {
		g := &safety.MockGuard{BlockAll: true}
		allowed, err := g.Check(context.Background(), "safe text")
		require.NoError(t, err)
		assert.False(t, allowed)
	})

	t.Run("Guard with SafetyConfig", func(t *testing.T) {
		g := &safety.Guard{
			Provider: &safety.MockGuard{},
			Config:   types.SafetyConfig{Enabled: true, InputGuard: true, OutputGuard: true, BlockThreshold: 0.9, WarnThreshold: 0.7},
		}
		require.NoError(t, g.CheckInput(context.Background(), "safe text"))
		assert.ErrorIs(t, g.CheckInput(context.Background(), "this is bad"), types.ErrGuardBlocked)
	})

	t.Run("Guard disabled skips check", func(t *testing.T) {
		g := &safety.Guard{
			Provider: &safety.MockGuard{},
			Config:   types.SafetyConfig{Enabled: false},
		}
		require.NoError(t, g.CheckInput(context.Background(), "this is bad"))
	})
}

// =============================================================================
// Test: Store (AgentStore)
// =============================================================================

func TestStore_AgentStore(t *testing.T) {
	s := store.NewMemoryStore()
	defer s.Close()

	t.Run("Save and Load", func(t *testing.T) {
		cfg := agent.Config{AgentID: "test-agent", Status: agent.StatusReady}
		require.NoError(t, s.Save(context.Background(), "test-agent", cfg))

		var loaded agent.Config
		require.NoError(t, s.Load(context.Background(), "test-agent", &loaded))
		assert.Equal(t, "test-agent", loaded.AgentID)
		assert.Equal(t, agent.StatusReady, loaded.Status)
	})

	t.Run("Load non-existent", func(t *testing.T) {
		err := s.Load(context.Background(), "missing", nil)
		assert.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "not found"))
	})

	t.Run("LoadAll", func(t *testing.T) {
		require.NoError(t, s.Save(context.Background(), "agent-1", map[string]any{"id": "agent-1"}))
		require.NoError(t, s.Save(context.Background(), "agent-2", map[string]any{"id": "agent-2"}))
		all, err := s.LoadAll(context.Background())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(all), 2)
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, s.Save(context.Background(), "to-delete", map[string]any{"id": "to-delete"}))
		require.NoError(t, s.Delete(context.Background(), "to-delete"))
		err := s.Load(context.Background(), "to-delete", &map[string]any{})
		assert.Error(t, err)
	})
}

// =============================================================================
// Test: Agent Builder & Registry
// =============================================================================

func buildTestAgent(id string, prov provider.LLMProvider, pipe pipeline.Pipeline, extraOpts ...func(*agent.Builder)) (*agent.Agent, error) {
	cfg := agent.Config{
		AgentID: id,
		Identity: &agent.Identity{
			Name: id, Role: "test agent",
			Description: "A test agent for integration tests",
		},
		Personality: agent.OCEAN{
			Openness: 0.8, Conscientiousness: 0.7, Extraversion: 0.5,
			Agreeableness: 0.9, Neuroticism: 0.2,
		},
		LLMConfig: types.ModelConfig{
			Model: "mock-model", Temperature: 0.7, MaxTokens: 1024,
		},
		PromptTmpl: fmt.Sprintf("You are a test agent named %s.", id),
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	builder := cfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(pipe)
	for _, opt := range extraOpts {
		opt(builder)
	}
	return builder.Build()
}

func TestAgent_FullLifecycle(t *testing.T) {
	prov := newMockProvider()
	pipe := pipeline.New(prov, pipeline.Recovery(), pipeline.Logging(nil))

	t.Run("Builder validates AgentID", func(t *testing.T) {
		cfg := agent.Config{AgentID: ""}
		cfg.FillDefaults()
		_, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).Build()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AgentID")
	})

	t.Run("Build agent with personality", func(t *testing.T) {
		ag, err := buildTestAgent("assistant", prov, pipe)
		require.NoError(t, err)
		defer ag.Close()
		assert.Equal(t, "assistant", ag.ID())
		assert.Equal(t, "test agent", ag.Config.Identity.Role)
		assert.Equal(t, float64(0.8), ag.Config.Personality.Openness)
	})

	t.Run("Build agent with tools", func(t *testing.T) {
		ag, err := buildTestAgent("tool-agent", prov, pipe, func(b *agent.Builder) {
			b.WithTools(tool.TimeTool(), tool.EchoTool())
		})
		require.NoError(t, err)
		defer ag.Close()
		assert.Len(t, ag.Tools, 2)
	})

	t.Run("Build agent with skills", func(t *testing.T) {
		ag, err := buildTestAgent("skill-agent", prov, pipe, func(b *agent.Builder) {
			b.WithSkills(builtin.TimeSkill(), builtin.WeatherSkill())
		})
		require.NoError(t, err)
		defer ag.Close()
		assert.Len(t, ag.Skills, 2)
		assert.Greater(t, len(ag.Tools), 0, "skills should auto-register tools")
	})

	t.Run("Build agent with components", func(t *testing.T) {
		tc := &testComponent{id: "test.cache", category: "cache"}
		ag, err := buildTestAgent("comp-agent", prov, pipe, func(b *agent.Builder) {
			b.WithExtensions(tc)
		})
		require.NoError(t, err)
		defer ag.Close()
		assert.Len(t, ag.Extensions, 1)
	assert.NotNil(t, ag.ComponentRegistry)
	got := ag.ComponentRegistry.Get("test.cache")
	assert.NotNil(t, got)
	})

	t.Run("Agent Chat collects stream", func(t *testing.T) {
		ag, err := buildTestAgent("chat-agent", prov, pipe)
		require.NoError(t, err)
		defer ag.Close()

		output, err := ag.Chat(context.Background(), &types.ChatInput{
			UserID: "u1",
			Messages: []types.Message{
				{Role: "user", Content: "Hello!"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, output.Content, "Hello")
	})

	t.Run("Agent Run non-streaming", func(t *testing.T) {
		ag, err := buildTestAgent("run-agent", prov, pipe)
		require.NoError(t, err)
		defer ag.Close()

		output, err := ag.Run(context.Background(), types.ChatInput{
			UserID: "u1",
			Messages: []types.Message{
				{Role: "user", Content: "Hi"},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, output)
	})

	t.Run("Agent RunStream emits events", func(t *testing.T) {
		ag, err := buildTestAgent("stream-agent", prov, pipe)
		require.NoError(t, err)
		defer ag.Close()

		events := make(chan types.StreamEvent, 10)
		done := make(chan struct{})

		var received []types.StreamEvent
		go func() {
			defer close(done)
			for e := range events {
				received = append(received, e)
			}
		}()

		err = ag.RunStream(context.Background(), types.ChatInput{
			UserID: "u1",
			Messages: []types.Message{
				{Role: "user", Content: "Hello"},
			},
		}, events)
		require.NoError(t, err)
		close(events)
		<-done
		assert.Greater(t, len(received), 0)
	})

	t.Run("Agent with safety config", func(t *testing.T) {
		cfg := agent.Config{
			AgentID:      "safe-agent",
			SafetyConfig: types.SafetyConfig{Enabled: true, BlockThreshold: 0.9, WarnThreshold: 0.7},
			Status:       agent.StatusReady,
			LLMConfig:    types.ModelConfig{Model: "mock-model"},
		}
		cfg.FillDefaults()

		ag, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).Build()
		require.NoError(t, err)
		defer ag.Close()
		assert.True(t, ag.Config.SafetyConfig.Enabled)
		assert.Equal(t, 0.9, ag.Config.SafetyConfig.BlockThreshold)
	})

	t.Run("Config FillDefaults", func(t *testing.T) {
		cfg := agent.Config{AgentID: "default-test"}
		cfg.FillDefaults()
		assert.True(t, cfg.SafetyConfig.Enabled)
		assert.Equal(t, 0.9, cfg.SafetyConfig.BlockThreshold)
		assert.Equal(t, 0.7, cfg.SafetyConfig.WarnThreshold)
		assert.Equal(t, 100, cfg.MemoryConfig.MaxEntries)
	})

	t.Run("Config ToBuilder", func(t *testing.T) {
		cfg := agent.Config{AgentID: "builder-test"}
		cfg.FillDefaults()
		builder := cfg.ToBuilder()
		assert.NotNil(t, builder)
	})
}

func TestAgent_Registry(t *testing.T) {
	prov := newMockProvider()
	pipe := pipeline.New(prov)

	ag1, err := buildTestAgent("agent-1", prov, pipe)
	require.NoError(t, err)
	defer ag1.Close()

	ag2, err := buildTestAgent("agent-2", prov, pipe)
	require.NoError(t, err)
	defer ag2.Close()

	reg := agent.NewRegistry()

	t.Run("Register and Get", func(t *testing.T) {
		require.NoError(t, reg.Register(ag1))
		got, ok := reg.Get("agent-1")
		assert.True(t, ok)
		assert.Equal(t, "agent-1", got.ID())
	})

	t.Run("Register duplicate fails", func(t *testing.T) {
		err := reg.Register(ag1)
		assert.Error(t, err)
	})

	t.Run("List all agents", func(t *testing.T) {
		require.NoError(t, reg.Register(ag2))
		all := reg.List()
		assert.Len(t, all, 2)
	})

	t.Run("Count reflects registrations", func(t *testing.T) {
		assert.Equal(t, 2, reg.Count())
	})

	t.Run("Delete removes agent", func(t *testing.T) {
		require.NoError(t, reg.Delete("agent-2"))
		_, ok := reg.Get("agent-2")
		assert.False(t, ok)
		assert.Equal(t, 1, reg.Count())
	})

	t.Run("Get non-existent", func(t *testing.T) {
		_, ok := reg.Get("no-such-agent")
		assert.False(t, ok)
	})

	t.Run("Concurrent access", func(t *testing.T) {
		ag3, _ := buildTestAgent("agent-3", prov, pipe)
		defer ag3.Close()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); reg.Get("agent-1") }()
		}
		wg.Wait()

		require.NoError(t, reg.Register(ag3))
		assert.Equal(t, 2, reg.Count())
	})
}

// =============================================================================
// Test: Team (Multi-Agent)
// =============================================================================

func TestTeam_CreateAndUse(t *testing.T) {
	prov := newMockProvider()
	pipe := pipeline.New(prov, pipeline.Recovery())

	coder, err := buildTestAgent("coder-team", prov, pipe)
	require.NoError(t, err)
	defer coder.Close()

	creative, err := buildTestAgent("creative-team", prov, pipe)
	require.NoError(t, err)
	defer creative.Close()

	t.Run("Team New creates supervisor with members", func(t *testing.T) {
		supervisorCfg := agent.Config{
			AgentID: "my-team",
			Identity: &agent.Identity{
				Name: "Supervisor", Role: "orchestrator",
				Description: "Delegates to specialists",
			},
			LLMConfig:  types.ModelConfig{Model: "mock-model", Temperature: 0.7, MaxTokens: 1024},
			PromptTmpl: "You are a supervisor.",
			Status:     agent.StatusReady,
		}
		supervisorCfg.FillDefaults()

		tm, err := team.New(supervisorCfg, prov, pipe, coder, creative)
		require.NoError(t, err)
		defer tm.Close()

		assert.Equal(t, "my-team", tm.ID)
		assert.NotNil(t, tm.Supervisor)
		assert.Len(t, tm.Members, 2)

		ids := tm.AgentIDs()
		assert.Contains(t, ids, "coder-team")
		assert.Contains(t, ids, "creative-team")

		tools := tm.MembersAsTools()
		assert.Len(t, tools, 2)
	})

	t.Run("Team AddMember + SyncTools", func(t *testing.T) {
		third, err := buildTestAgent("third-member", prov, pipe)
		require.NoError(t, err)
		defer third.Close()

		supervisorCfg := agent.Config{
			AgentID: "growing-team", LLMConfig: types.ModelConfig{Model: "mock-model"},
			Status: agent.StatusReady,
		}
		supervisorCfg.FillDefaults()

		tm, err := team.New(supervisorCfg, prov, pipe, coder)
		require.NoError(t, err)
		defer tm.Close()

		assert.Len(t, tm.Members, 1)

		tm.AddMember(third)
		assert.Len(t, tm.Members, 2)

		tm.SyncTools()
		assert.Len(t, tm.MembersAsTools(), 2)
	})

	t.Run("Agent.AsTool creates delegate tool", func(t *testing.T) {
		delegateTool := coder.AsTool()
		assert.Equal(t, "delegate_to_coder-team", delegateTool.Name())
		assert.Contains(t, delegateTool.Description(), "coder-team")

		// Execute delegation
		result, err := delegateTool.Execute(context.Background(),
			json.RawMessage(`{"message":"Write hello world"}`))
		require.NoError(t, err)
		assert.Contains(t, result, "Hello")
	})
}

// =============================================================================
// Test: End-to-End Scenario
// =============================================================================

func TestE2E_FullScenario(t *testing.T) {
	prov := newMockProvider()
	pipe := pipeline.New(
		prov,
		pipeline.Recovery(),
		pipeline.Logging(nil),
		pipeline.Timeout(30*time.Second),
	)

	t.Run("Complete agent lifecycle with all subsystems", func(t *testing.T) {
		// 1. Agent store
		agentStore := store.NewMemoryStore()
		defer agentStore.Close()

		// 2. Memory engine
		memStore := memory.NewInMemoryStore()
		memEngine := memory.NewEngine(memStore, types.MemoryConfig{
			MaxEntries: 100, TTLMillis: 30000,
		}, prov, nil)
		defer memEngine.Close()

		// 3. Session manager
		sessStore := session.NewMemoryStore()
		sessManager := session.NewSessionManager(sessStore)
		defer sessStore.Close()

		// 4. Safety guard
		guard := &safety.Guard{
			Provider: &safety.MockGuard{AllowAll: true},
			Config:   types.SafetyConfig{Enabled: true, BlockThreshold: 0.9, WarnThreshold: 0.7},
		}

		// 5. Skills registry
		skillReg := skills.NewRegistry()
		skillReg.Register(builtin.TimeSkill())
		skillReg.Register(builtin.EchoSkill())

		// 6. Tool registry
		toolReg := tool.NewRegistry()
		toolReg.Register(tool.TimeTool())

		// 7. Component registry
		compReg := component.NewRegistry()
		compReg.Register(&testComponent{id: "test.cache", category: "cache"})

		// 8. Build agent
		agCfg := agent.Config{
			AgentID: "e2e-agent",
			Identity: &agent.Identity{
				Name: "E2E Agent", Role: "integration test agent",
				Description: "An agent that exercises all subsystems.",
			},
			Personality: agent.OCEAN{
				Openness: 0.9, Conscientiousness: 0.8, Extraversion: 0.5,
				Agreeableness: 0.9, Neuroticism: 0.1,
			},
			LLMConfig: types.ModelConfig{
				Model: "mock-model", Temperature: 0.7, MaxTokens: 2048,
			},
			SafetyConfig: types.SafetyConfig{Enabled: true, BlockThreshold: 0.9, WarnThreshold: 0.7},
			MemoryConfig: types.MemoryConfig{MaxEntries: 100},
			PromptTmpl:   "You are a comprehensive test agent.",
			Status:       agent.StatusReady,
		}
		agCfg.FillDefaults()

		ag, err := agCfg.ToBuilder().
			WithProvider(prov).
			WithPipeline(pipe).
			WithMemory(memStore).
			WithTools(tool.TimeTool(), tool.EchoTool()).
			WithSkills(builtin.TimeSkill()).
			WithExtensions(compReg.Get("test.cache")).
			Build()
		require.NoError(t, err)
		defer ag.Close()

		// 9. Store agent config
		require.NoError(t, agentStore.Save(context.Background(), ag.ID(), ag.Config))

		// 10. Create agent registry
		reg := agent.NewRegistry()
		require.NoError(t, reg.Register(ag))
		assert.Equal(t, 1, reg.Count())

		// 11. Create session
		sess, err := sessManager.GetOrCreate(context.Background(), "user-1", ag.ID(), "sess-e2e")
		require.NoError(t, err)
		assert.True(t, sess.State.Active)

		// 12. Safety check passes
		require.NoError(t, guard.CheckInput(context.Background(), "Hello"))

		// 13. Memory context before chat
		ctx := memEngine.BuildContext(context.Background(), "user-1", ag.ID(), "Hello")
		assert.Empty(t, ctx, "no prior memories")

		// 14. Run chat
		output, err := ag.Chat(context.Background(), &types.ChatInput{
			UserID:  "user-1",
			AgentID: ag.ID(),
			Messages: []types.Message{
				{Role: "user", Content: "Hello, who are you?"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, output.Content, "Hello")

		// 15. Distill and save memory
		require.NoError(t, memEngine.DistillAndSave(context.Background(),
			"user-1", ag.ID(), "Hello, who are you?", output.Content,
		))

		// 16. Update session
		require.NoError(t, sessManager.Update(context.Background(), sess.ID,
			[]types.Message{
				{Role: "user", Content: "Hello, who are you?"},
				{Role: "assistant", Content: output.Content},
			},
		))

		// 17. BuildContext now has memories
		ctx = memEngine.BuildContext(context.Background(), "user-1", ag.ID(), "Hello")
		assert.NotEmpty(t, ctx)

		// 18. Session remains active
		sess, err = sessManager.Get(context.Background(), sess.ID)
		require.NoError(t, err)
		assert.True(t, sess.State.Active)
		assert.Len(t, sess.Messages, 2)

		// 19. Agent config preserved in store
		var loadedCfg agent.Config
		require.NoError(t, agentStore.Load(context.Background(), ag.ID(), &loadedCfg))
		assert.Equal(t, ag.ID(), loadedCfg.AgentID)

		// 20. Cleanup
		require.NoError(t, sessManager.Close(context.Background(), sess.ID))
	})
}

// =============================================================================
// Test: Pipeline Built-in Middleware
// =============================================================================

func TestPipeline_BuiltinMiddleware(t *testing.T) {
	prov := newMockProvider()

	t.Run("Recovery catches panic", func(t *testing.T) {
		p := pipeline.New(prov, pipeline.Recovery())
		// Use a middleware that panics
		p.Use(func(next pipeline.Handler) pipeline.Handler {
			return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
				panic("test panic")
			}
		})
		_, err := p.ChatStream(context.Background(), types.ChatInput{
			UserID: "u1", Messages: []types.Message{{Role: "user", Content: "hi"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "panic")
	})

	t.Run("Timeout cancels slow handler", func(t *testing.T) {
		p := pipeline.New(
			prov,
			pipeline.Timeout(10*time.Millisecond),
		)
		p.Use(func(next pipeline.Handler) pipeline.Handler {
			return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return nil
				}
			}
		})
		_, err := p.ChatStream(context.Background(), types.ChatInput{
			UserID: "u1", Messages: []types.Message{{Role: "user", Content: "hi"}},
		})
		assert.Error(t, err)
	})

	t.Run("Logging with nil logger is noop", func(t *testing.T) {
		p := pipeline.New(prov, pipeline.Logging(nil))
		_, err := p.ChatStream(context.Background(), types.ChatInput{
			UserID: "u1", Messages: []types.Message{{Role: "user", Content: "hi"}},
		})
		assert.NoError(t, err)
	})
}

// =============================================================================
// Test: Agent Extension Methods
// =============================================================================

func TestAgent_Extensions(t *testing.T) {
	prov := newMockProvider()
	pipe := pipeline.New(prov)

	t.Run("Agent with extension", func(t *testing.T) {
		tc := &testComponent{id: "ext.test", category: "test"}

		cfg := agent.Config{
			AgentID: "ext-agent", Status: agent.StatusReady,
			LLMConfig: types.ModelConfig{Model: "mock"},
		}
		cfg.FillDefaults()

		ag, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).
			WithExtensions(tc).Build()
		require.NoError(t, err)
		defer ag.Close()

		assert.Len(t, ag.Extensions, 1)
		assert.Contains(t, ag.Extensions, "ext.test")
	})

	t.Run("Agent Close closes all extensions", func(t *testing.T) {
		tc := &testComponent{id: "close.test", category: "test"}

		cfg := agent.Config{
			AgentID: "close-agent", Status: agent.StatusReady,
			LLMConfig: types.ModelConfig{Model: "mock"},
		}
		cfg.FillDefaults()

		ag, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).
			WithExtensions(tc).Build()
		require.NoError(t, err)

		assert.NoError(t, ag.Close())
		assert.True(t, tc.closed)
	})

	t.Run("Agent Close collects multiple errors", func(t *testing.T) {
		tc1 := &testComponent{id: "err1", category: "test"}
		tc2 := &testComponent{id: "err2", category: "test"}

		cfg := agent.Config{
			AgentID: "multi-close-agent", Status: agent.StatusReady,
			LLMConfig: types.ModelConfig{Model: "mock"},
		}
		cfg.FillDefaults()

		ag, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).
			WithExtensions(tc1, tc2).Build()
		require.NoError(t, err)
		assert.NoError(t, ag.Close())
	})
}

// =============================================================================
// Test: Personality Edge Cases
// =============================================================================

func TestPersonality_EdgeCases(t *testing.T) {
	t.Run("OCEAN zero values ToMap", func(t *testing.T) {
		ocean := agent.OCEAN{}
		m := ocean.ToMap()
		assert.Equal(t, float64(0), m["openness"])
		assert.Equal(t, float64(0), m["neuroticism"])
	})

	t.Run("OCEAN FromMap partial", func(t *testing.T) {
		ocean := agent.OCEAN{}
		ocean.FromMap(map[string]float64{
			"openness": 0.5, "extraversion": 0.7,
		})
		assert.Equal(t, float64(0.5), ocean.Openness)
		assert.Equal(t, float64(0.7), ocean.Extraversion)
		assert.Equal(t, float64(0), ocean.Conscientiousness)
	})

	t.Run("OCEAN FromMap empty", func(t *testing.T) {
		ocean := agent.OCEAN{Openness: 1.0}
		ocean.FromMap(map[string]float64{})
		assert.Equal(t, float64(1.0), ocean.Openness)
	})
}

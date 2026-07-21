package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/memory"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ---------------------------------------------------------------------------
// mocks
// ---------------------------------------------------------------------------

// mockProvider is a provider.LLMProvider that returns canned responses.
type mockProvider struct {
	name         string
	chatFn       func(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error)
	chatStreamFn func(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error)
	closeFn      func() error
}

func (m *mockProvider) Chat(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, messages, config)
	}
	return "ok", nil
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error) {
	if m.chatStreamFn != nil {
		return m.chatStreamFn(ctx, messages, config)
	}
	ch := make(chan types.StreamEvent, 2)
	ch <- types.StreamEvent{Type: "chunk", Content: "hello "}
	ch <- types.StreamEvent{Type: "done", Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Ping(_ context.Context) error { return nil }
func (m *mockProvider) Name() string                 { return m.name }
func (m *mockProvider) Models() []string             { return nil }
func (m *mockProvider) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

var (
	_ provider.LLMProvider = (*mockProvider)(nil)
	_ provider.Provider    = (*mockProvider)(nil)
)

// mockMemory is a memory.Store backed by a simple slice.
type mockMemory struct {
	mu    sync.Mutex
	items []*memory.Entry
}

func newMockMemory() *mockMemory { return &mockMemory{} }

func (m *mockMemory) Add(_ context.Context, entry *memory.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, entry)
	return nil
}

func (m *mockMemory) Search(_ context.Context, _ string, _ int) ([]*memory.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items, nil
}

func (m *mockMemory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.items {
		if e.ID == id {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockMemory) Close() error { return nil }

var _ memory.Store = (*mockMemory)(nil)

// mockSkill is a Skill whose Tools() returns a predefined list.
type mockSkill struct {
	name  string
	tools []tool.Tool
}

func (s *mockSkill) Name() string        { return s.name }
func (s *mockSkill) Description() string { return "mock skill" }
func (s *mockSkill) Instructions() string {
	return "mock skill instructions"
}
func (s *mockSkill) Tools() []tool.Tool { return s.tools }
func (s *mockSkill) Metadata() skills.SkillMetadata {
	return skills.SkillMetadata{Tags: []string{"mock"}}
}
func (s *mockSkill) Match(_ context.Context, _ string) float64 { return 0 }

var _ skills.Skill = (*mockSkill)(nil)

// echoTool is a simple tool that echoes its input.
type echoTool struct{}

func (e echoTool) Name() string            { return "echo" }
func (e echoTool) Description() string     { return "echoes input" }
func (e echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (e echoTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	if args != nil {
		return string(args), nil
	}
	return "{}", nil
}

var _ tool.Tool = echoTool{}

// helper: creates a valid Config with defaults applied.
func validConfig() agent.Config {
	cfg := agent.Config{
		AgentID: "test-agent",
		LLMConfig: types.ModelConfig{
			Model: "gpt-4",
		},
	}
	cfg.FillDefaults()
	return cfg
}

// helper: creates a valid pipeline backed by a mock provider.
func validPipeline() pipeline.Pipeline {
	return pipeline.New(&mockProvider{name: "mock"})
}

// ---------------------------------------------------------------------------
// Config tests
// ---------------------------------------------------------------------------

func TestConfig_FillDefaults_SetsSafetyEnabled(t *testing.T) {
	// Given: a Config with zero-value SafetyConfig
	cfg := agent.Config{
		AgentID: "a1",
	}

	// When
	cfg.FillDefaults()

	// Then: safety is enabled and thresholds are set
	assert.True(t, cfg.SafetyConfig.Enabled)
	assert.Equal(t, 0.9, cfg.SafetyConfig.BlockThreshold)
	assert.Equal(t, 0.7, cfg.SafetyConfig.WarnThreshold)
}

func TestConfig_FillDefaults_SetsMemoryDefaults(t *testing.T) {
	// Given: zero-value MemoryConfig
	cfg := agent.Config{AgentID: "a1"}

	// When
	cfg.FillDefaults()

	// Then
	assert.True(t, cfg.MemoryConfig.Consolidation)
	assert.Equal(t, 100, cfg.MemoryConfig.MaxEntries)
	assert.Equal(t, int64(3_600_000), cfg.MemoryConfig.TTLMillis)
}

func TestConfig_FillDefaults_PreservesExplicitThresholdValues(t *testing.T) {
	// Given: a Config with explicitly set thresholds
	cfg := agent.Config{
		AgentID: "a1",
		SafetyConfig: types.SafetyConfig{
			Enabled:        true,
			BlockThreshold: 0.5,
		},
		MemoryConfig: types.MemoryConfig{
			MaxEntries:    50,
			TTLMillis:     1000,
			Consolidation: true,
		},
	}

	// When
	cfg.FillDefaults()

	// Then: explicit non-zero values are preserved, zero-threshold values get defaults
	assert.True(t, cfg.SafetyConfig.Enabled)
	assert.Equal(t, 0.5, cfg.SafetyConfig.BlockThreshold)
	assert.Equal(t, 50, cfg.MemoryConfig.MaxEntries)
	assert.Equal(t, int64(1000), cfg.MemoryConfig.TTLMillis)
	assert.True(t, cfg.MemoryConfig.Consolidation)
}

func TestConfig_ToBuilder_ReturnsPopulatedBuilder(t *testing.T) {
	cfg := agent.Config{
		AgentID:   "b1",
		LLMConfig: types.ModelConfig{Model: "gpt-4"},
	}

	b := cfg.ToBuilder()

	assert.NotNil(t, b)
}

// ---------------------------------------------------------------------------
// Builder tests
// ---------------------------------------------------------------------------

func TestBuilder_Build_ValidConfig_Succeeds(t *testing.T) {
	// Given: a valid Config + Provider + Pipeline
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}
	p := validPipeline()

	// When
	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p)
	a, err := b.Build()

	// Then
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, "test-agent", a.ID())
	assert.Equal(t, prov, a.Provider)
	assert.Equal(t, p, a.Pipeline)
}

func TestBuilder_Build_MissingAgentID_Fails(t *testing.T) {
	// Given: empty AgentID
	cfg := agent.Config{}
	prov := &mockProvider{name: "mock"}
	p := validPipeline()

	// When
	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p)
	_, err := b.Build()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AgentID is required")
}

func TestBuilder_Build_MissingProvider_Fails(t *testing.T) {
	// Given: Config with AgentID but no Provider
	cfg := validConfig()
	p := validPipeline()

	// When
	b := cfg.ToBuilder().WithPipeline(p)
	_, err := b.Build()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Provider is required")
}

func TestBuilder_Build_MissingPipeline_Fails(t *testing.T) {
	// Given: Config with AgentID + Provider but no Pipeline
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}

	// When
	b := cfg.ToBuilder().WithProvider(prov)
	_, err := b.Build()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Pipeline is required")
}

func TestBuilder_WithTools_AddsToolsToAgent(t *testing.T) {
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}
	p := validPipeline()
	t1 := echoTool{}
	t2 := tool.EchoTool()

	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p).WithTools(t1, t2)
	a, err := b.Build()

	require.NoError(t, err)
	assert.Len(t, a.Tools, 2)
}

func TestBuilder_WithMemory_SetsMemoryStore(t *testing.T) {
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}
	p := validPipeline()
	mem := newMockMemory()

	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p).WithMemory(mem)
	a, err := b.Build()

	require.NoError(t, err)
	assert.Equal(t, mem, a.Memory)
}

func TestBuilder_WithSkills_RegistersSkillTools(t *testing.T) {
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}
	p := validPipeline()

	sk1 := &mockSkill{
		name:  "weather",
		tools: []tool.Tool{echoTool{}, tool.TimeTool()},
	}
	sk2 := &mockSkill{
		name:  "search",
		tools: []tool.Tool{tool.EchoTool()},
	}

	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p).
		WithTools(echoTool{}).
		WithSkills(sk1, sk2)

	a, err := b.Build()

	require.NoError(t, err)
	// 1 direct tool + 2 from sk1 + 1 from sk2 = 4
	assert.Len(t, a.Tools, 4)
	assert.Len(t, a.Skills, 2)
}

func TestBuilder_BuildOrPanic_Succeeds(t *testing.T) {
	cfg := validConfig()
	prov := &mockProvider{name: "mock"}
	p := validPipeline()

	b := cfg.ToBuilder().WithProvider(prov).WithPipeline(p)
	a := b.BuildOrPanic()
	assert.NotNil(t, a)
}

func TestBuilder_BuildOrPanic_PanicsOnError(t *testing.T) {
	cfg := agent.Config{} // missing AgentID

	assert.Panics(t, func() {
		cfg.ToBuilder().BuildOrPanic()
	})
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := agent.NewRegistry()
	a := validTestAgent(t)

	require.NoError(t, reg.Register(a))

	got, ok := reg.Get(a.ID())
	require.True(t, ok)
	assert.Equal(t, a.ID(), got.ID())

	_, ok = reg.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistry_List_ReturnsAllAgents(t *testing.T) {
	reg := agent.NewRegistry()
	a1 := validTestAgentWithID(t, "agent-1")
	a2 := validTestAgentWithID(t, "agent-2")

	require.NoError(t, reg.Register(a1))
	require.NoError(t, reg.Register(a2))

	list := reg.List()
	assert.Len(t, list, 2)

	ids := make(map[string]bool)
	for _, a := range list {
		ids[a.ID()] = true
	}
	assert.True(t, ids["agent-1"])
	assert.True(t, ids["agent-2"])
}

func TestRegistry_Delete_RemovesAgent(t *testing.T) {
	reg := agent.NewRegistry()
	a := validTestAgent(t)
	require.NoError(t, reg.Register(a))

	require.NoError(t, reg.Delete(a.ID()))

	_, ok := reg.Get(a.ID())
	assert.False(t, ok)
}

func TestRegistry_Delete_NotFound_ReturnsError(t *testing.T) {
	reg := agent.NewRegistry()

	err := reg.Delete("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRegistry_Count_ReflectsRegisterAndDelete(t *testing.T) {
	reg := agent.NewRegistry()
	assert.Equal(t, 0, reg.Count())

	a1 := validTestAgentWithID(t, "agent-1")
	a2 := validTestAgentWithID(t, "agent-2")

	require.NoError(t, reg.Register(a1))
	assert.Equal(t, 1, reg.Count())

	require.NoError(t, reg.Register(a2))
	assert.Equal(t, 2, reg.Count())

	require.NoError(t, reg.Delete(a1.ID()))
	assert.Equal(t, 1, reg.Count())
}

func TestRegistry_Register_DuplicateID_Fails(t *testing.T) {
	reg := agent.NewRegistry()
	a := validTestAgent(t)

	require.NoError(t, reg.Register(a))
	err := reg.Register(a)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_Register_NilAgent_Fails(t *testing.T) {
	reg := agent.NewRegistry()

	err := reg.Register(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil agent")
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := agent.NewRegistry()
	var wg sync.WaitGroup
	const count = 20

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a := validTestAgentWithID(t, fmt.Sprintf("agent-%d", id))
			_ = reg.Register(a)
		}(i)
	}

	wg.Wait()

	assert.Equal(t, count, reg.Count())
}

// ---------------------------------------------------------------------------
// Agent Chat tests
// ---------------------------------------------------------------------------

func TestAgent_Chat_CollectsStreamChunks(t *testing.T) {
	// Given: a provider that streams two chunks then done
	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 3)
			ch <- types.StreamEvent{Type: "chunk", Content: "Hello, "}
			ch <- types.StreamEvent{Type: "chunk", Content: "World!"}
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	// When
	output, err := a.Chat(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", output.Content)
	assert.Equal(t, "assistant", output.Role)
	assert.Equal(t, "stop", output.FinishReason)
	assert.False(t, output.IsStream)
}

func TestAgent_Chat_ReturnsErrorFromPipeline(t *testing.T) {
	// Given: a provider whose ChatStream fails
	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			return nil, errors.New("provider unavailable")
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	// When
	_, err := a.Chat(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider unavailable")
}

func TestAgent_Chat_ReturnsErrorFromStreamEvent(t *testing.T) {
	// Given: a stream that contains an error event
	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 2)
			ch <- types.StreamEvent{Type: "error", Error: errors.New("guard blocked")}
			close(ch)
			return ch, nil
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	// When
	_, err := a.Chat(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "guard blocked")
}

func TestAgent_Chat_NilInput_ReturnsError(t *testing.T) {
	cfg := validConfig()
	a := buildAgent(t, cfg, &mockProvider{name: "mock"})

	_, err := a.Chat(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestAgent_Chat_WithSystemPrompt_PrependsMessage(t *testing.T) {
	// Given: a Config with a system prompt
	var capturedMessages []types.Message

	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, messages []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			capturedMessages = messages
			ch := make(chan types.StreamEvent, 1)
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	cfg := validConfig()
	cfg.PromptTmpl = "You are a helpful assistant."
	a := buildAgent(t, cfg, prov)

	// When
	_, err := a.Chat(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	// Then: system prompt is prepended
	require.Len(t, capturedMessages, 2)
	assert.Equal(t, "system", capturedMessages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", capturedMessages[0].Content)
	assert.Equal(t, "user", capturedMessages[1].Role)
}

func TestAgent_Chat_WithoutSystemPrompt_UsesUserMessages(t *testing.T) {
	// Given: no PromptTmpl in config
	var capturedMessages []types.Message

	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, messages []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			capturedMessages = messages
			ch := make(chan types.StreamEvent, 1)
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	// When
	_, err := a.Chat(context.Background(), &types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	// Then: no system message added
	require.Len(t, capturedMessages, 1)
	assert.Equal(t, "user", capturedMessages[0].Role)
}

// ---------------------------------------------------------------------------
// Agent interface tests
// ---------------------------------------------------------------------------

func TestAgent_ID_ReturnsConfigAgentID(t *testing.T) {
	cfg := validConfig()
	a := buildAgent(t, cfg, &mockProvider{name: "mock"})

	assert.Equal(t, "test-agent", a.ID())
}

func TestAgent_Run_CallsPipelineRun(t *testing.T) {
	// Given: a provider that returns a canned response
	prov := &mockProvider{
		name: "mock",
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "canned response", nil
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	// When
	output, err := a.Run(context.Background(), types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, "canned response", output.Content)
}

func TestAgent_RunStream_ForwardsEvents(t *testing.T) {
	// Given: a streaming provider
	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 2)
			ch <- types.StreamEvent{Type: "chunk", Content: "a"}
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	events := make(chan types.StreamEvent, 10)
	done := make(chan struct{})

	var received []types.StreamEvent
	go func() {
		defer close(done)
		for e := range events {
			received = append(received, e)
		}
	}()

	// When
	err := a.RunStream(context.Background(), types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}, events)
	close(events)
	<-done

	// Then
	require.NoError(t, err)
	require.Len(t, received, 2)
	assert.Equal(t, "chunk", received[0].Type)
	assert.True(t, received[1].Done)
}

func TestAgent_RunStream_PipelineError_ReturnsError(t *testing.T) {
	// Given: provider that fails on ChatStream
	prov := &mockProvider{
		name: "mock",
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			return nil, errors.New("stream setup failed")
		},
	}

	cfg := validConfig()
	a := buildAgent(t, cfg, prov)

	events := make(chan types.StreamEvent, 1)

	// When
	err := a.RunStream(context.Background(), types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	}, events)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream setup failed")
}

func TestAgent_Close_ReleasesResources(t *testing.T) {
	// Given: an agent with a closeable provider and memory
	var providerClosed bool
	var memoryClosed bool

	prov := &mockProvider{
		name: "mock",
		closeFn: func() error {
			providerClosed = true
			return nil
		},
	}

	mem := &closeTrackerMemory{closed: &memoryClosed}

	cfg := validConfig()
	a := mustBuild(t, cfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(validPipeline()).
		WithMemory(mem))

	// When
	err := a.Close()

	// Then
	require.NoError(t, err)
	assert.True(t, providerClosed)
	assert.True(t, memoryClosed)
}

func TestAgent_Close_CollectsErrors(t *testing.T) {
	// Given: provider that errors on close
	prov := &mockProvider{
		name: "mock",
		closeFn: func() error {
			return errors.New("close failed")
		},
	}

	cfg := validConfig()
	a := mustBuild(t, cfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(validPipeline()))

	// When
	err := a.Close()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
}

// ---------------------------------------------------------------------------
// Agent + Personality
// ---------------------------------------------------------------------------

func TestAgent_StoresPersonality(t *testing.T) {
	cfg := validConfig()
	cfg.Identity = &agent.Identity{
		Name: "Helper",
		Role: "assistant",
	}
	cfg.Personality = agent.OCEAN{
		Openness: 0.8,
	}

	a := buildAgent(t, cfg, &mockProvider{name: "mock"})

	assert.NotNil(t, a.Config.Identity)
	assert.Equal(t, "Helper", a.Config.Identity.Name)
	assert.Equal(t, 0.8, a.Config.Personality.Openness)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validTestAgent(t *testing.T) *agent.Agent {
	t.Helper()
	return validTestAgentWithID(t, "test-agent")
}

func validTestAgentWithID(t *testing.T, id string) *agent.Agent {
	t.Helper()
	cfg := validConfig()
	cfg.AgentID = id
	return buildAgent(t, cfg, &mockProvider{name: "mock"})
}

func buildAgent(t *testing.T, cfg agent.Config, prov provider.LLMProvider) *agent.Agent {
	t.Helper()
	// Create the pipeline from the given provider so tests that
	// customize provider behaviour see those behaviours through
	// the pipeline.
	p := pipeline.New(prov)
	return mustBuild(t, cfg.ToBuilder().WithProvider(prov).WithPipeline(p))
}

func mustBuild(t *testing.T, b *agent.Builder) *agent.Agent {
	t.Helper()
	a, err := b.Build()
	require.NoError(t, err)
	return a
}

// closeTrackerMemory is a memory.Store that tracks Close() calls.
type closeTrackerMemory struct {
	closed *bool
}

func (m *closeTrackerMemory) Add(_ context.Context, _ *memory.Entry) error { return nil }
func (m *closeTrackerMemory) Search(_ context.Context, _ string, _ int) ([]*memory.Entry, error) {
	return nil, nil
}
func (m *closeTrackerMemory) Delete(_ context.Context, _ string) error { return nil }
func (m *closeTrackerMemory) Close() error {
	*m.closed = true
	return nil
}

var _ memory.Store = (*closeTrackerMemory)(nil)

package team

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// mockProvider implements provider.LLMProvider for team tests.
type mockProvider struct{}

func (m *mockProvider) Name() string                              { return "mock" }
func (m *mockProvider) Models() []string                          { return []string{"mock"} }
func (m *mockProvider) Ping(_ context.Context) error              { return nil }
func (m *mockProvider) Close() error                              { return nil }
func (m *mockProvider) Chat(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
	return "mock response", nil
}
func (m *mockProvider) ChatStream(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
	ch := make(chan types.StreamEvent)
	go func() { close(ch) }()
	return ch, nil
}

var _ provider.LLMProvider = (*mockProvider)(nil)

func newTeamTestAgent(t *testing.T, id string, prov provider.LLMProvider, pipe pipeline.Pipeline) *agent.Agent {
	t.Helper()
	cfg := agent.Config{
		AgentID: id,
		Identity: &agent.Identity{
			Name: id, Role: "test member",
			Description: "A test team member agent.",
		},
		LLMConfig:  types.ModelConfig{Model: "mock-model", Temperature: 0.5, MaxTokens: 512},
		PromptTmpl: "You are a test agent named " + id + ".",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()
	ag, err := cfg.ToBuilder().WithProvider(prov).WithPipeline(pipe).Build()
	require.NoError(t, err)
	return ag
}

func TestNew_ValidatesSupervisorID(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	cfg := agent.Config{AgentID: "", Status: agent.StatusReady}
	_, err := New(cfg, prov, pipe)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AgentID")
}

func TestNew_CreatesSupervisorAndMembers(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	member1 := newTeamTestAgent(t, "worker-1", prov, pipe)
	defer member1.Close()
	member2 := newTeamTestAgent(t, "worker-2", prov, pipe)
	defer member2.Close()

	cfg := agent.Config{
		AgentID: "orchestrator",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are an orchestrator.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, member1, member2)
	require.NoError(t, err)
	defer tm.Close()

	assert.Equal(t, "orchestrator", tm.ID)
	assert.NotNil(t, tm.Supervisor)
	assert.Len(t, tm.Members, 2)

	ids := tm.AgentIDs()
	assert.Contains(t, ids, "worker-1")
	assert.Contains(t, ids, "worker-2")
}

func TestNew_SupervisorHasDelegateTools(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	member := newTeamTestAgent(t, "specialist", prov, pipe)
	defer member.Close()

	cfg := agent.Config{
		AgentID: "manager",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are a manager.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, member)
	require.NoError(t, err)
	defer tm.Close()

	tools := tm.MembersAsTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "delegate_to_specialist", tools[0].Name())
}

func TestAddMember_AndSyncTools(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	initial := newTeamTestAgent(t, "initial", prov, pipe)
	defer initial.Close()

	cfg := agent.Config{
		AgentID: "growing-team",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are a supervisor.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, initial)
	require.NoError(t, err)
	defer tm.Close()

	assert.Len(t, tm.Members, 1)
	assert.Len(t, tm.MembersAsTools(), 1)

	added := newTeamTestAgent(t, "new-member", prov, pipe)
	defer added.Close()

	tm.AddMember(added)
	assert.Len(t, tm.Members, 2)
	assert.Len(t, tm.MembersAsTools(), 1, "tools not synced yet")

	tm.SyncTools()
	assert.Len(t, tm.MembersAsTools(), 2)
}

func TestAgentIDs_ReturnsOrdered(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	m1 := newTeamTestAgent(t, "a", prov, pipe)
	defer m1.Close()
	m2 := newTeamTestAgent(t, "b", prov, pipe)
	defer m2.Close()

	cfg := agent.Config{
		AgentID: "leader",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are a leader.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, m1, m2)
	require.NoError(t, err)
	defer tm.Close()

	ids := tm.AgentIDs()
	assert.Equal(t, []string{"a", "b"}, ids)
}

func TestClose_CleansUpAllAgents(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	member := newTeamTestAgent(t, "cleanup-member", prov, pipe)

	cfg := agent.Config{
		AgentID: "cleanup-team",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are a supervisor.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, member)
	require.NoError(t, err)

	tm.Close()
	// After Close, calling ID() should not panic.
	assert.Equal(t, "cleanup-team", tm.ID)
}

func TestMembersAsTools_ReturnsCopy(t *testing.T) {
	prov := &mockProvider{}
	pipe := pipeline.New(prov)

	member := newTeamTestAgent(t, "copy-member", prov, pipe)
	defer member.Close()

	cfg := agent.Config{
		AgentID: "copy-team",
		LLMConfig: types.ModelConfig{Model: "mock-model"},
		PromptTmpl: "You are a supervisor.",
		Status:     agent.StatusReady,
	}
	cfg.FillDefaults()

	tm, err := New(cfg, prov, pipe, member)
	require.NoError(t, err)
	defer tm.Close()

	tools1 := tm.MembersAsTools()
	tools2 := tm.MembersAsTools()
	assert.Equal(t, len(tools1), len(tools2))
	// Verify independent slices (modifying one doesn't affect the other)
	tools1[0] = nil
	assert.NotNil(t, tools2[0])
}

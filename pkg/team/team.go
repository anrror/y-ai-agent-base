// Package team provides multi-agent team orchestration with a supervisor
// pattern. A Team groups several specialized agents under a supervisor agent
// that can delegate tasks to any member via tool calling.
//
// The supervisor agent is automatically configured with a "delegate_to_<id>"
// tool for each member. When the LLM calls one of these tools, the task is
// forwarded to the corresponding member agent and the response is returned
// as the tool result.
//
// Usage:
//
//	team, err := team.New(
//	    supervisorCfg,
//	    prov, pipe,
//	    memberAgent1, memberAgent2, memberAgent3,
//	)
//	// chat with the team via the supervisor
//	output, err := team.Supervisor.Chat(ctx, &types.ChatInput{...})
//
// Members can also be added after construction:
//
//	team.AddMember(anotherAgent)
//	team.SyncTools()  // rebuilds supervisor's tool set
package team

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

// Team represents a multi-agent team with a supervisor that delegates to
// specialized member agents via function calling.
type Team struct {
	mu sync.Mutex

	// ID is the unique identifier for this team, used for routing.
	ID string

	// Supervisor is the orchestrator agent that has each member registered
	// as a callable tool. All chat interactions go through the supervisor.
	Supervisor *agent.Agent

	// Members are the specialized worker agents that can be delegated to.
	Members []*agent.Agent

	// toolsCache holds the current set of delegate tools synced to the
	// supervisor. Used to avoid redundant rebuilds.
	toolsCache []tool.Tool
}

// New creates a Team from a supervisor config, provider, pipeline, and
// member agents. The supervisor agent is built with the given config and
// has each member registered as a "delegate_to_<id>" tool.
//
// The supervisorCfg.AgentID is used as the Team ID. Members are added
// in the order they are provided.
//
// To add members after construction, use team.AddMember() then
// team.SyncTools().
func New(
	supervisorCfg agent.Config,
	prov provider.LLMProvider,
	pipe pipeline.Pipeline,
	members ...*agent.Agent,
) (*Team, error) {
	if supervisorCfg.AgentID == "" {
		return nil, fmt.Errorf("team: supervisor AgentID is required")
	}

	t := &Team{
		ID:      supervisorCfg.AgentID,
		Members: make([]*agent.Agent, 0, len(members)),
	}

	// Build supervisor.
	supervisorCfg.FillDefaults()
	supervisor, err := supervisorCfg.ToBuilder().
		WithProvider(prov).
		WithPipeline(pipe).
		Build()
	if err != nil {
		return nil, fmt.Errorf("team: build supervisor: %w", err)
	}

	t.Supervisor = supervisor

	// Add members and sync tools.
	t.Members = append(t.Members, members...)
	t.SyncTools()

	return t, nil
}

// AddMember adds a member agent to the team. Does NOT automatically sync
// tools — call SyncTools() after all additions are complete.
func (t *Team) AddMember(m *agent.Agent) {
	if m == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Members = append(t.Members, m)
}

// SyncTools rebuilds the supervisor's delegation tools from the current
// member list and attaches them to the supervisor. Call after any
// AddMember / RemoveMember mutation.
func (t *Team) SyncTools() {
	t.mu.Lock()
	defer t.mu.Unlock()
	tools := make([]tool.Tool, 0, len(t.Members))
	for _, m := range t.Members {
		tools = append(tools, m.AsTool())
	}
	t.toolsCache = tools
	t.Supervisor.AddTools(tools...)
}

// MembersAsTools returns the current delegate tools for all members.
func (t *Team) MembersAsTools() []tool.Tool {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]tool.Tool, len(t.toolsCache))
	copy(result, t.toolsCache)
	return result
}

// AgentIDs returns the IDs of all member agents.
func (t *Team) AgentIDs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, len(t.Members))
	for i, m := range t.Members {
		ids[i] = m.ID()
	}
	return ids
}

// Close calls Close on all member agents and the supervisor.
func (t *Team) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []string
	for _, m := range t.Members {
		if err := m.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("member %q: %v", m.ID(), err))
		}
	}
	if err := t.Supervisor.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("supervisor: %v", err))
	}
	if len(errs) > 0 {
		slog.Warn("team close errors", "team_id", t.ID, "errors", strings.Join(errs, "; "))
	}
}

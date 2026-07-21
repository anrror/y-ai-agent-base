package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// agentTool adapts an Agent into a callable Tool for multi-agent delegation.
// When an LLM calls this tool, the message is forwarded to the target agent's
// Chat loop (with full tool-calling support), and the response is returned.
type agentTool struct {
	agent *Agent
}

func (t *agentTool) Name() string {
	return "delegate_to_" + t.agent.ID()
}

func (t *agentTool) Description() string {
	desc := t.agent.Config.Identity.Description
	if desc == "" {
		desc = fmt.Sprintf("Delegate a task to the %q agent", t.agent.ID())
	}
	return fmt.Sprintf("Delegate a task to %s. %s", t.agent.ID(), desc)
}

func (t *agentTool) Schema() json.RawMessage {
	return tool.NewParamSchema().
		AddString("message", "The task or question to delegate to this agent", true).
		Build()
}

func (t *agentTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("%w: %w", tool.ErrInvalidArgs, err)
	}
	if params.Message == "" {
		return "", fmt.Errorf("%w: message is required", tool.ErrInvalidArgs)
	}

	output, err := t.agent.Chat(ctx, &types.ChatInput{
		Messages: []types.Message{
			{Role: "user", Content: params.Message},
		},
	})
	if err != nil {
		return "", fmt.Errorf("agent %q delegation failed: %w", t.agent.ID(), err)
	}
	return output.Content, nil
}

// AsTool converts the Agent into a tool.Tool that other agents (or the
// supervisor) can invoke via function calling. The tool is named
// "delegate_to_<agent_id>" and accepts a single "message" string parameter.
//
// Usage:
//
//	coder := registry.Get("coder")
//	supervisor.AddTools(coder.AsTool())
//
// Now when the supervisor agent's LLM calls delegate_to_coder(message=...),
// the message is forwarded to the coder agent's Chat loop and the response
// is returned as the tool result.
func (a *Agent) AsTool() tool.Tool {
	return &agentTool{agent: a}
}

// AddTools adds one or more tools to the agent's tool set at runtime.
// This enables dynamic attachment of delegation tools (or any other tool)
// after the agent has been built, without rebuilding.
//
// Safe to call after Build(). Tools are picked up by the next Chat() call
// since Chat() reads a.Tools on every iteration.
func (a *Agent) AddTools(tools ...tool.Tool) {
	a.mu.Lock()
	a.Tools = append(a.Tools, tools...)
	a.mu.Unlock()
}

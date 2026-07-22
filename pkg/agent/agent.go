// Package agent defines the Agent type, builder, configuration, and registry.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anrror/y-ai-agent-base/pkg/component"
	"github.com/anrror/y-ai-agent-base/pkg/knowledge"
	"github.com/anrror/y-ai-agent-base/pkg/mcp"
	"github.com/anrror/y-ai-agent-base/pkg/memory"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Agent is the core agent struct that orchestrates LLM calls through a
// pipeline, with optional tools, memory, skills, and external extensions.
type Agent struct {
	mu sync.RWMutex // guards Config and Provider during hot-reload

	Config   Config
	Provider provider.LLMProvider
	Pipeline pipeline.Pipeline
	Tools    []tool.Tool
	Memory   memory.Store
	Skills   []Skill

	// MCPRegistry holds the MCP server connections for dynamic tool
	// resolution during Chat(). When nil, MCP tool resolution is skipped.
	MCPRegistry *mcp.Registry

	// Extensions holds external modules (emotion, reasoning, scheduler,
	// cache, compressor, edge, driver, etc.) attached via the Builder's
	// WithExtensions() method. Keyed by Extension.ID().
	Extensions map[string]Extension

	// ComponentRegistry holds all Extensions that also implement the
	// Component interface. It is populated during Build() and used for
	// cross-component discovery (Lookup / LookupAll).
	ComponentRegistry *component.Registry
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	return a.Config.AgentID
}

// ReloadConfig atomically replaces the agent's configuration after applying
// defaults. It refuses to change the AgentID. Safe for concurrent use.
func (a *Agent) ReloadConfig(cfg Config) error {
	cfg.FillDefaults()

	a.mu.Lock()
	defer a.mu.Unlock()

	if cfg.AgentID != a.Config.AgentID {
		return fmt.Errorf("agent: cannot change AgentID via reload: %q != %q", cfg.AgentID, a.Config.AgentID)
	}
	a.Config = cfg
	return nil
}

// GetConfig returns a copy of the agent's current configuration.
// Safe for concurrent use.
func (a *Agent) GetConfig() Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Config
}

// ReloadProvider atomically replaces the agent's LLM provider and pipeline
// under the agent's mutex. This is safe for concurrent use with Chat() and
// other methods that read Provider/Pipeline.
func (a *Agent) ReloadProvider(prov provider.LLMProvider, pipe pipeline.Pipeline) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Provider = prov
	a.Pipeline = pipe
}

// maxToolIterations caps the number of sequential tool-call rounds
// to prevent infinite loops.
const maxToolIterations = 10

const roleTool = "tool"

// Chat sends a chat request through the pipeline's streaming interface,
// collects all stream chunks, and executes any tool calls the LLM requests.
// It loops until the LLM returns a text response (no tool_calls), or until
// maxToolIterations is reached.
//
// Tenant identity: if input.AgentID is empty it is auto-filled from the
// agent's Config.AgentID. UserID, when set, is propagated through context
// to downstream middleware, memory, and session stores.
func (a *Agent) Chat(ctx context.Context, input *types.ChatInput) (*types.ChatOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("agent: ChatInput must not be nil")
	}

	ctx = a.injectTenant(ctx, input)

	a.mu.RLock()
	req := a.prepareRequest(input)
	tools := make([]tool.Tool, len(a.Tools))
	copy(tools, a.Tools)
	provider := a.Provider
	pipeline := a.Pipeline
	mcpReg := a.MCPRegistry
	a.mu.RUnlock()

	// Resolve MCP tools: merge agent config with session-level overrides.
	mcpTools, err := resolveSessionMCP(mcpReg, a.Config.MCP, input.MCP)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve MCP: %w", err)
	}

	// Combine base tools + MCP tools for this session.
	sessionTools := make([]tool.Tool, 0, len(tools)+len(mcpTools))
	sessionTools = append(sessionTools, tools...)
	sessionTools = append(sessionTools, mcpTools...)

	for i := 0; i < maxToolIterations; i++ {
		// Attach tools to the provider before each call.
		if len(sessionTools) > 0 {
			if tp, ok := provider.(interface{ SetTools([]tool.Tool) }); ok {
				tp.SetTools(sessionTools)
			}
		}

		output, err := pipeline.ChatStream(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("agent: iteration %d: %w", i, err)
		}

		// If the pipeline returned a non-streaming response, return as-is.
		if !output.IsStream || output.Stream == nil {
			return &output, nil
		}

		// Collect stream events, checking for tool_calls.
		result, toolCalls, err := a.collectStreamWithTools(output)
		if err != nil {
			return nil, fmt.Errorf("agent: iteration %d: %w", i, err)
		}

		// No tool calls → final text response.
		if len(toolCalls) == 0 {
			return result, nil
		}

		// Append the assistant message with tool_calls to the conversation.
		req.Messages = append(req.Messages, types.Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: toolCalls,
		})

		// Execute each tool and append tool result messages.
		for _, tc := range toolCalls {
			toolMsg := a.executeTool(ctx, tc, sessionTools)
			req.Messages = append(req.Messages, toolMsg)
		}
	}

	return nil, fmt.Errorf("agent: exceeded max tool call iterations (%d)", maxToolIterations)
}

// executeTool looks up a tool by name and executes it, returning a "tool"
// role message with the result or error.
func (a *Agent) executeTool(ctx context.Context, tc types.ToolCall, tools []tool.Tool) types.Message {
	for _, t := range tools {
		if t.Name() == tc.Function.Name {
			result, err := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				return types.Message{
					Role:       roleTool,
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    fmt.Sprintf(`{"error":%q}`, err.Error()),
				}
			}
			return types.Message{
				Role:       roleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			}
		}
	}
	// Tool not found among agent's tools.
	return types.Message{
		Role:       roleTool,
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Content:    fmt.Sprintf(`{"error":"tool %q not found"}`, tc.Function.Name),
	}
}

// GetExtension returns the extension registered under the given id, or nil
// if no extension with that id exists. The caller should type-assert to the
// concrete extension type:
//
//	if ext := a.GetExtension("emotion"); ext != nil {
//	    em := ext.(*myapp.EmotionExt)
//	    em.Detect(...)
//	}
//
// Safe for concurrent use (the map is set once at build time and never
// mutated after).
func (a *Agent) GetExtension(id string) Extension {
	if a.Extensions == nil {
		return nil
	}
	return a.Extensions[id]
}

// Knowledge returns the Knowledge component attached to this agent,
// or nil when the agent has no knowledge capability.
//
// Safe for concurrent use (the Extensions map is set once at build time
// and never mutated after).
func (a *Agent) Knowledge() *knowledge.Knowledge {
	ext := a.GetExtension(knowledge.ComponentID)
	if ext == nil {
		return nil
	}
	k, _ := ext.(*knowledge.Knowledge)
	return k
}

// GetComponent returns the Component registered under the given id from the
// ComponentRegistry, or nil if no Component with that id exists. Components
// are a superset of Extensions — they have a lifecycle (Init/Close), belong
// to a Category, and support cross-component discovery.
//
// Usage:
//
//	c := agent.GetComponent("cache.redis")
//	if c != nil {
//	    eng := c.(interface{ Engines() []component.Component }).Engines()
//	}
//
// Safe for concurrent use (the registry is populated once at build time).
func (a *Agent) GetComponent(id string) component.Component {
	if a.ComponentRegistry == nil {
		return nil
	}
	return a.ComponentRegistry.Get(id)
}

// Run executes a synchronous (non-streaming) pipeline run.
// Tenant identity is propagated via injectTenant (see Chat).
func (a *Agent) Run(ctx context.Context, input types.ChatInput) (types.ChatOutput, error) {
	ctx = a.injectTenant(ctx, &input)
	req := a.prepareRequest(&input)

	a.mu.RLock()
	pipe := a.Pipeline
	a.mu.RUnlock()

	output, err := pipe.Run(ctx, req)
	if err != nil {
		return types.ChatOutput{}, fmt.Errorf("agent run: %w", err)
	}
	return output, nil
}

// RunStream executes a streaming pipeline run and forwards every event
// to the caller-provided channel. The caller is responsible for closing
// the events channel after RunStream returns.
// Tenant identity is propagated via injectTenant (see Chat).
func (a *Agent) RunStream(ctx context.Context, input types.ChatInput, events chan<- types.StreamEvent) error {
	ctx = a.injectTenant(ctx, &input)
	req := a.prepareRequest(&input)

	a.mu.RLock()
	pipe := a.Pipeline
	a.mu.RUnlock()

	output, err := pipe.ChatStream(ctx, req)
	if err != nil {
		return fmt.Errorf("agent: pipeline ChatStream: %w", err)
	}

		if !output.IsStream || output.Stream == nil {
		select {
		case events <- types.StreamEvent{Type: "chunk", Content: output.Content}:
		case <-ctx.Done():
			return fmt.Errorf("agent: send chunk: %w", ctx.Err())
		}
		select {
		case events <- types.StreamEvent{Type: "done", Done: true}:
		case <-ctx.Done():
			return fmt.Errorf("agent: send done: %w", ctx.Err())
		}
		return nil
	}

	for evt := range output.Stream {
		if evt.Error != nil {
			return evt.Error
		}
		select {
		case events <- evt:
		case <-ctx.Done():
			return fmt.Errorf("agent: send stream event: %w", ctx.Err())
		}
	}
	return nil
}

// Close releases resources held by the agent, including all registered
// extensions. Errors are collected; all Close() calls are attempted even
// when one fails. Safe for multiple calls.
func (a *Agent) Close() error {
	var errs []string

	if a.Memory != nil {
		if err := a.Memory.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("memory: %v", err))
		}
	}
	if a.Provider != nil {
		if closer, ok := a.Provider.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("provider: %v", err))
			}
		}
	}
	for _, ext := range a.Extensions {
		if err := ext.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("extension %q: %v", ext.ID(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("agent: close errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// injectTenant enriches the context with tenant identity from ChatInput.
// If input.AgentID is empty it is filled from the agent's own Config.AgentID.
// The caller's ChatInput is modified in place so that downstream middleware
// and prepareRequest see a consistent view.
func (a *Agent) injectTenant(ctx context.Context, input *types.ChatInput) context.Context {
	if input.AgentID == "" {
		input.AgentID = a.Config.AgentID
	}
	ctx = context.WithValue(ctx, types.CtxAgentID, input.AgentID)
	if input.UserID != "" {
		ctx = context.WithValue(ctx, types.CtxUserID, input.UserID)
	}
	return ctx
}

// prepareRequest builds a ChatInput from the agent's config and the
// caller's input, merging defaults and injecting the system prompt.
func (a *Agent) prepareRequest(input *types.ChatInput) types.ChatInput {
	modelCfg := &a.Config.LLMConfig
	if input.ModelConfig != nil {
		modelCfg = input.ModelConfig
	}

	safetyCfg := &a.Config.SafetyConfig
	if input.SafetyConfig != nil {
		safetyCfg = input.SafetyConfig
	}

	messages := a.buildMessages(input.Messages)

	return types.ChatInput{
		Messages:     messages,
		ModelConfig:  modelCfg,
		SafetyConfig: safetyCfg,
		Tools:        input.Tools,
		Metadata:     input.Metadata,
		Timeout:      input.Timeout,
		AgentID:      input.AgentID,
		UserID:       input.UserID,
	}
}

// buildMessages prepends the system prompt (when configured) to the
// user-provided messages.
func (a *Agent) buildMessages(userMessages []types.Message) []types.Message {
	if a.Config.PromptTmpl == "" {
		// Return a copy to prevent the caller's append from corrupting
		// the original slice's backing array.
		cp := make([]types.Message, len(userMessages))
		copy(cp, userMessages)
		return cp
	}

	messages := make([]types.Message, 0, len(userMessages)+1)
	messages = append(messages, types.Message{
		Role:    "system",
		Content: a.Config.PromptTmpl,
	})
	messages = append(messages, userMessages...)
	return messages
}

// collectStreamWithTools reads all events from a streaming ChatOutput and returns
// a consolidated ChatOutput plus any tool calls the LLM requested.
func (a *Agent) collectStreamWithTools(output types.ChatOutput) (*types.ChatOutput, []types.ToolCall, error) {
	var content strings.Builder
	var finishReason string
	var toolCalls []types.ToolCall

	for evt := range output.Stream {
		if evt.Error != nil {
			return nil, nil, fmt.Errorf("agent: stream error: %w", evt.Error)
		}
		if evt.Content != "" {
			content.WriteString(evt.Content)
		}
		if len(evt.ToolCalls) > 0 {
			toolCalls = append(toolCalls, evt.ToolCalls...)
		}
		if evt.Done {
			finishReason = "stop"
		}
	}

	return &types.ChatOutput{
		Content:      content.String(),
		Role:         "assistant",
		FinishReason: finishReason,
		IsStream:     false,
	}, toolCalls, nil
}

// Ensure Agent implements the legacy Agent interface (kept for
// consumers that still type-assert against it).
type _agentInterface interface {
	ID() string
	Run(ctx context.Context, input types.ChatInput) (types.ChatOutput, error)
	RunStream(ctx context.Context, input types.ChatInput, events chan<- types.StreamEvent) error
	Close() error
}

var _ _agentInterface = (*Agent)(nil)

// resolveSessionMCP resolves the effective MCP tool set by merging
// the agent's default MCP config with optional per-session overrides.
//
// Priority (highest wins):
//  1. Session overrides (when input.MCP != nil)
//  2. Agent config defaults (Config.MCP)
//
// When session overrides explicitly disable MCP (Enabled=false),
// no MCP tools are returned regardless of agent config.
func resolveSessionMCP(reg *mcp.Registry, agentCfg MCPConfig, sessionCfg *types.MCPSessionConfig) ([]tool.Tool, error) {
	if reg == nil {
		return nil, nil
	}

	// Determine effective enabled flag.
	enabled := agentCfg.Enabled
	if sessionCfg != nil && sessionCfg.Enabled != nil {
		enabled = *sessionCfg.Enabled
	}
	if !enabled {
		return nil, nil
	}

	// Determine effective server list.
	servers := agentCfg.Servers
	if sessionCfg != nil && sessionCfg.Servers != nil {
		servers = sessionCfg.Servers
	}

	return mcp.ResolveTools(reg, servers)
}

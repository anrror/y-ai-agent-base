package agent

import (
	"errors"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/component"
	"github.com/anrror/y-ai-agent-base/pkg/knowledge"
	"github.com/anrror/y-ai-agent-base/pkg/mcp"
	"github.com/anrror/y-ai-agent-base/pkg/memory"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

// Builder constructs an Agent from a Config with optional dependencies.
// Call Config.ToBuilder() or create directly, then chain With* methods
// before calling Build().
type Builder struct {
	config     Config
	provider   provider.LLMProvider
	pipeline   pipeline.Pipeline
	tools      []tool.Tool
	memory     memory.Store
	skills     []Skill
	extensions []Extension
	mcpReg     *mcp.Registry
	mcpTools   []tool.Tool // pre-resolved MCP tools (lazy)
}

// WithProvider sets the LLM provider for the agent.
func (b *Builder) WithProvider(p provider.LLMProvider) *Builder {
	b.provider = p
	return b
}

// WithPipeline sets the processing pipeline for the agent.
func (b *Builder) WithPipeline(p pipeline.Pipeline) *Builder {
	b.pipeline = p
	return b
}

// WithTools attaches the given tools to the agent.
func (b *Builder) WithTools(tools ...tool.Tool) *Builder {
	b.tools = append(b.tools, tools...)
	return b
}

// WithMemory sets the memory store for the agent.
func (b *Builder) WithMemory(store memory.Store) *Builder {
	b.memory = store
	return b
}

// WithSkills attaches skills to the agent. Each skill's tools are
// automatically registered with the agent.
func (b *Builder) WithSkills(skills ...Skill) *Builder {
	b.skills = append(b.skills, skills...)
	return b
}

// WithExtensions attaches external modules (emotion, reasoning, scheduler,
// cache, compressor, edge, driver, etc.) to the agent. Each extension must
// have a non-empty ID. Duplicate IDs are allowed — later items override
// earlier ones.
//
// If an extension also implements MiddlewareProvider, its middleware is
// automatically injected into the agent's pipeline during Build().
func (b *Builder) WithExtensions(exts ...Extension) *Builder {
	b.extensions = append(b.extensions, exts...)
	return b
}

// WithKnowledge attaches a knowledge store to the agent as a
// Knowledge component (extension). Each agent independently decides
// whether to use knowledge — agents without WithKnowledge() simply
// have no knowledge capability.
//
// The Knowledge component is registered under the ID "knowledge" and
// can be retrieved at runtime via Agent.Knowledge().
//
// Example:
//
//	store := knowledge.NewInMemoryStore()
//	ag, err := ac.ToBuilder().
//	    WithProvider(prov).
//	    WithPipeline(pipe).
//	    WithKnowledge(knowledge.New(store, knowledge.DefaultConfig())).
//	    Build()
//
// Or with auto-inject enabled:
//
//	cfg := knowledge.Config{AutoInject: true, TopK: 5}
//	ag, err := ac.ToBuilder().
//	    WithProvider(prov).
//	    WithPipeline(pipe).
//	    WithKnowledge(knowledge.New(store, cfg)).
//	    Build()
func (b *Builder) WithKnowledge(kn *knowledge.Knowledge) *Builder {
	if kn != nil {
		b.extensions = append(b.extensions, kn)
	}
	return b
}

// WithMCPRegistry sets the MCP server registry for this agent.
//
// The registry holds MCP server connections managed by the host system.
// During Build(), the agent resolves tools from the servers listed in
// its Config.MCP.Servers (or all servers when the list is empty).
//
// Example:
//
//	reg := mcp.NewRegistry()
//	reg.Add(mcp.NewServer("fs", myFSClient))
//
//	cfg := agent.Config{
//	    AgentID: "assistant",
//	    MCP: agent.MCPConfig{
//	        Enabled: true,
//	        Servers: []string{"fs"},
//	    },
//	}
//	ag, _ := cfg.ToBuilder().
//	    WithProvider(prov).
//	    WithPipeline(pipe).
//	    WithMCPRegistry(reg).
//	    Build()
func (b *Builder) WithMCPRegistry(reg *mcp.Registry) *Builder {
	b.mcpReg = reg
	return b
}

// WithComponent is a convenience method that delegates to WithExtensions.
// Every Component implements Extension, so this is purely for API clarity:
// use WithExtensions for general-purpose modules, and WithComponent when
// you specifically want a Component's lifecycle (Init/Close) and category
// registration to take effect.
func (b *Builder) WithComponent(comps ...component.Component) *Builder {
	for _, c := range comps {
		b.extensions = append(b.extensions, c)
	}
	return b
}

// Build validates the configuration and constructs an Agent.
// Returns an error when AgentID is missing, or Provider is nil,
// or Pipeline is nil.
func (b *Builder) Build() (*Agent, error) {
	if b.config.AgentID == "" {
		return nil, errors.New("agent: Config.AgentID is required")
	}
	if b.provider == nil {
		return nil, errors.New("agent: Provider is required")
	}
	if b.pipeline == nil {
		return nil, errors.New("agent: Pipeline is required")
	}

	// Collect tools from skills into the agent's tool list.
	allTools := make([]tool.Tool, len(b.tools))
	copy(allTools, b.tools)
	for _, sk := range b.skills {
		allTools = append(allTools, sk.Tools()...)
	}

	// Build extensions map, collect MiddlewareProvider extensions,
	// and collect tools from ToolProvider extensions.
	exts := make(map[string]Extension, len(b.extensions))
	var extMWs []pipeline.Middleware
	for _, ext := range b.extensions {
		if ext.ID() == "" {
			return nil, errors.New("agent: extension with empty ID")
		}
		exts[ext.ID()] = ext
		if mp, ok := ext.(MiddlewareProvider); ok {
			extMWs = append(extMWs, mp.Middleware())
		}
		if tp, ok := ext.(ToolProvider); ok {
			allTools = append(allTools, tp.Tools()...)
		}
	}

	// Resolve MCP tools from registry when MCP is enabled.
	if b.config.MCP.Enabled && b.mcpReg != nil {
		mcpTools, err := mcp.ResolveTools(b.mcpReg, b.config.MCP.Servers)
		if err != nil {
			return nil, fmt.Errorf("agent: resolve MCP tools: %w", err)
		}
		allTools = append(allTools, mcpTools...)
	}

	// Wrap the shared pipeline with per-agent extension middlewares to
	// prevent cross-contamination: without wrapping, b.pipeline.Use() would
	// mutate the shared pipeline, causing Agent A's extension middleware
	// to also be applied to Agent B's requests.
	effectivePipeline := b.pipeline
	if len(extMWs) > 0 {
		pw := &pipelineWrapper{inner: b.pipeline}
		pw.Use(extMWs...)
		effectivePipeline = pw
	}

	// Build component registry from Extensions that implement Component,
	// then initialise each in priority order.
	compReg := component.NewRegistry()
	for _, ext := range exts {
		if c, ok := ext.(component.Component); ok {
			compReg.Register(c)
		}
	}

	if compReg.Len() > 0 {
		initCtx := &component.InitContext{
			Pipeline: effectivePipeline,
			Lookup:   compReg.Get,
			LookupAll: compReg.ListByCategory,
		}
		for _, c := range compReg.List() {
			if err := c.Init(initCtx); err != nil {
				return nil, fmt.Errorf("agent: component %q init: %w", c.ID(), err)
			}
		}
	}

	return &Agent{
		Config:            b.config,
		Provider:          b.provider,
		Pipeline:          effectivePipeline,
		Tools:             allTools,
		Memory:            b.memory,
		Skills:            b.skills,
		MCPRegistry:       b.mcpReg,
		Extensions:        exts,
		ComponentRegistry: compReg,
	}, nil
}

// BuildOrPanic calls Build and panics on error. Useful in tests and
// startup code where failure is non-recoverable.
func (b *Builder) BuildOrPanic() *Agent {
	a, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("agent BuildOrPanic: %v", err))
	}
	return a
}

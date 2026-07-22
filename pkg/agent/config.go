package agent

import (
	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// AgentStatus represents the current life-cycle phase of an agent.
type AgentStatus string

const (
	StatusInitializing AgentStatus = "initializing"
	StatusReady        AgentStatus = "ready"
	StatusRunning      AgentStatus = "running"
	StatusStopped      AgentStatus = "stopped"
	StatusError        AgentStatus = "error"
)

// Skill is a type alias for the skills.Skill interface from pkg/skills.
// This is kept for backward compatibility with existing code.
type Skill = skills.Skill

// OCEAN represents the Big Five personality traits.
// Each dimension ranges from 0.0 (low) to 1.0 (high).
type OCEAN struct {
	Openness          float64 `json:"openness"`
	Conscientiousness float64 `json:"conscientiousness"`
	Extraversion      float64 `json:"extraversion"`
	Agreeableness     float64 `json:"agreeableness"`
	Neuroticism       float64 `json:"neuroticism"`
}

// ToMap converts OCEAN traits to a map keyed by trait name.
func (o OCEAN) ToMap() map[string]float64 {
	return map[string]float64{
		"openness":          o.Openness,
		"conscientiousness": o.Conscientiousness,
		"extraversion":      o.Extraversion,
		"agreeableness":     o.Agreeableness,
		"neuroticism":       o.Neuroticism,
	}
}

// FromMap populates an OCEAN value from a map keyed by trait name.
// Missing keys leave the corresponding field unchanged.
func (o *OCEAN) FromMap(m map[string]float64) {
	if v, ok := m["openness"]; ok {
		o.Openness = v
	}
	if v, ok := m["conscientiousness"]; ok {
		o.Conscientiousness = v
	}
	if v, ok := m["extraversion"]; ok {
		o.Extraversion = v
	}
	if v, ok := m["agreeableness"]; ok {
		o.Agreeableness = v
	}
	if v, ok := m["neuroticism"]; ok {
		o.Neuroticism = v
	}
}

// Identity defines an agent's persona and behavioural constraints.
type Identity struct {
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	Description    string   `json:"description,omitempty"`
	Tone           string   `json:"tone,omitempty"`
	Verbosity      string   `json:"verbosity,omitempty"`
	Constraints    []string `json:"constraints,omitempty"`
	ExamplePrompts []string `json:"example_prompts,omitempty"`
}

// Config holds the configuration for an Agent.
type Config struct {
	AgentID      string            `json:"agent_id"`
	Identity     *Identity         `json:"identity,omitempty"`
	Personality  OCEAN             `json:"personality,omitempty"`
	LLMConfig    types.ModelConfig `json:"llm_config"`
	MemoryConfig types.MemoryConfig    `json:"memory_config,omitempty"`
	SafetyConfig types.SafetyConfig    `json:"safety_config,omitempty"`
	Status       AgentStatus       `json:"status"`
	PromptTmpl   string            `json:"prompt_tmpl,omitempty"`
	MCP          MCPConfig         `json:"mcp,omitempty"`
}

// MCPConfig controls MCP (Model Context Protocol) tool integration
// for this agent. MCP allows the agent to discover and call tools
// from external MCP servers.
//
// The actual MCP server connections are managed by the host system
// through a *mcp.Registry passed to the Builder.
type MCPConfig struct {
	// Enabled enables MCP tool integration for this agent.
	// When false (default), no MCP tools are attached.
	Enabled bool `json:"enabled"`

	// Servers lists the MCP server names (from the Registry) that
	// this agent may use. An empty slice means "use all available
	// servers in the registry".
	Servers []string `json:"servers,omitempty"`
}

// FillDefaults sets sensible defaults on optional configuration blocks.
//
// NOTE: SafetyConfig.Enabled is unconditionally set to true when it is false.
// Because Go's bool zero-value and an explicit "false" are indistinguishable
// at the struct level, FillDefaults cannot tell whether the user intentionally
// disabled safety or simply left the field unset. A proper fix would require
// changing Enabled to *bool (pointer) so nil means "unset" and false means
// "explicitly disabled", but that changes the public Config API. For now,
// safety is always on by default and cannot be disabled through FillDefaults.
func (c *Config) FillDefaults() {
	if !c.SafetyConfig.Enabled {
		c.SafetyConfig.Enabled = true
	}
	if c.SafetyConfig.BlockThreshold == 0 {
		c.SafetyConfig.BlockThreshold = 0.9
	}
	if c.SafetyConfig.WarnThreshold == 0 {
		c.SafetyConfig.WarnThreshold = 0.7
	}
	if c.MemoryConfig.MaxEntries == 0 {
		c.MemoryConfig.MaxEntries = 100
	}
	if c.MemoryConfig.TTLMillis == 0 {
		c.MemoryConfig.TTLMillis = 3_600_000 // 1 hour
	}
	if !c.MemoryConfig.Consolidation {
		c.MemoryConfig.Consolidation = true
	}
}

// ToBuilder returns a Builder pre-populated with this Config.
func (c Config) ToBuilder() *Builder {
	return &Builder{config: c}
}

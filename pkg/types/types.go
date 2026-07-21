// Package types defines shared core domain types used across the agent framework.
package types

import "time"

// Message represents a single chat message.
// The "tool" role is used for tool execution results; "assistant" messages
// may carry ToolCalls when the model requests tool invocations.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // "tool" role: the id of the tool call this result responds to
	Name       string     `json:"name,omitempty"`         // "tool" role: function name this result is for
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // "assistant" role: tool calls requested by the model
}

// ToolCall represents an LLM-requested tool invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and JSON-encoded arguments of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ModelConfig configures a language model provider.
type ModelConfig struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	Temperature    float64 `json:"temperature"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	TopP           float64 `json:"top_p,omitempty"`
	Seed           *int    `json:"seed,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"` // per-model timeout override (seconds)
}

// MemoryConfig configures memory retention settings.
type MemoryConfig struct {
	MaxEntries    int   `json:"max_entries"`
	TTLMillis     int64 `json:"ttl_ms"`
	Consolidation bool  `json:"consolidation"`
}

// MemoryEntry is a single item stored in memory.
type MemoryEntry struct {
	ID         string         `json:"id"`
	Content    string         `json:"content"`
	Importance float64        `json:"importance"`
	CreatedAt  time.Time      `json:"created_at"`
	AccessedAt time.Time      `json:"accessed_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// SessionState holds session-level state.
type SessionState struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	AgentID   string         `json:"agent_id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Active    bool           `json:"active"`
}

// ChatInput is the input for a chat completion request.
type ChatInput struct {
	Messages     []Message      `json:"messages"`
	ModelConfig  *ModelConfig   `json:"model_config,omitempty"`
	SafetyConfig *SafetyConfig  `json:"safety_config,omitempty"`
	Tools        []string       `json:"tools,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Timeout      time.Duration  `json:"timeout,omitempty"` // per-request timeout, 0 = use default or model config

	// Tenant identity — together they scope sessions, memory, metrics.
	//
	// AgentID identifies the target agent persona. When empty, Agent.Chat()
	// auto-fills it from the agent's own Config.AgentID.
	//
	// UserID identifies the requesting tenant user. Both are optional —
	// when empty the framework runs in single-tenant mode and downstream
	// stores (memory, session) operate without multi-tenant scoping.
	AgentID string `json:"agent_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

// ChatOutput is the result of a chat completion.
// IsStream defaults to false.
type ChatOutput struct {
	Content      string             `json:"content"`
	Role         string             `json:"role"`
	FinishReason string             `json:"finish_reason,omitempty"`
	IsStream     bool               `json:"is_stream"`
	Stream       <-chan StreamEvent `json:"-"` // only populated when IsStream is true; not JSON-serialized
	Model        string             `json:"model,omitempty"`
	Usage        *UsageInfo         `json:"usage,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
}

// UsageInfo tracks token consumption.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// SafetyConfig configures content safety guardrails.
type SafetyConfig struct {
	Enabled        bool     `json:"enabled"`
	InputGuard     bool     `json:"input_guard"`
	OutputGuard    bool     `json:"output_guard"`
	BlockThreshold float64  `json:"block_threshold,omitempty"`
	WarnThreshold  float64  `json:"warn_threshold,omitempty"`
	Categories     []string `json:"categories,omitempty"`
}

// GuardResult is the outcome of a content safety check.
type GuardResult struct {
	Allowed  bool           `json:"allowed"`
	Score    float64        `json:"score"`
	Category string         `json:"category,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

// ChatCompletionRequest mirrors a standard LLM chat completion request.
type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
	Tools       []any     `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
}

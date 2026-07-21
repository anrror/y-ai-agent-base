// Package driver implements the LLM driving layer — the central composition
// hub that applies parameter tuning, token-budget management, retry with
// circuit breaker, prompt rendering, and metrics recording around every
// LLM invocation.
//
// The Driver is designed to be generic: business-specific concerns (emotion,
// personality, etc.) flow through the Call.Metadata map and are interpreted
// by the ParameterTuner implementation supplied by the consumer.
//
// Architecture
//
//	Driver
//	  ├── ParameterTuner        — adjusts LLM params from Call.Metadata
//	  ├── TokenBudgetManager    — context-window budget allocation
//	  ├── RetryStrategy         — exponential backoff + circuit breaker
//	  ├── MetricsCollector      — latency, tokens, error recording
//	  └── PromptEngine          — named Go-template prompt rendering
package driver

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Top-level Driver interface
// ---------------------------------------------------------------------------

// Call describes one LLM invocation with all context the Driver needs.
// Metadata carries business-specific data (emotion, personality, etc.) that
// the consumer's ParameterTuner implementation can interpret.
type Call struct {
	SystemPrompt string
	Messages     []Message

	// Metadata carries arbitrary business context for downstream processors
	// (tuners, budget managers, etc.). Keys are consumer-defined; the
	// framework never reads from Metadata directly.
	Metadata map[string]any

	// TokenBudget is the output of TokenBudgetManager.Allocate; nil means
	// no budget enforcement.
	TokenBudget *Budget

	// TuningOverride bypasses automatic tuning when non-nil.
	TuningOverride *TuningParams

	// Model selection.
	Model    string
	Provider string // preferred provider name
}

// Message is a lightweight message for the driver layer.
type Message struct {
	Role    string
	Content string
	Name    string
}

// Result captures the full outcome of a driver execution.
type Result struct {
	Content      string
	TuningParams TuningParams
	TokenUsage   TokenUsage
	RetryCount   int
	Duration     time.Duration
	Provider     string
	Model        string
}

// TokenUsage tracks LLM token consumption.
type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}

// TuningParams holds the runtime parameters passed to the LLM.
type TuningParams struct {
	Temperature      float64
	TopP             float64
	MaxTokens        int
	PresencePenalty  float64
	FrequencyPenalty float64
}

// Config controls the Driver's behaviour.
type Config struct {
	DefaultModel       string
	DefaultProvider    string
	MaxRetries         int
	RetryBaseDelay     time.Duration
	EnableTuning       bool
	EnableBudget       bool
	EnableMetrics      bool
	CircuitBreakerThreshold int
	CircuitBreakerCooldown   time.Duration
}

// Driver is the central execution hub. It receives a Call, executes the LLM
// invocation through tuning, retry, and budget enforcement, and returns the
// Result.
type Driver interface {
	// Execute runs a single LLM call through the driving layer.
	Execute(ctx context.Context, call *Call, llmFn LLMFunc) (*Result, error)

	// Metrics returns the current aggregated metrics snapshot.
	Metrics(ctx context.Context) (*MetricsSnapshot, error)

	// Configure hot-reloads the driver configuration.
	Configure(cfg Config) error
}

// LLMFunc is the fundamental LLM invocation primitive.
type LLMFunc func(ctx context.Context, system string, messages []Message, params TuningParams) (*Result, error)

// ---------------------------------------------------------------------------
// Sub-system interfaces
// ---------------------------------------------------------------------------

// ParameterTuner adjusts LLM call parameters based on the business context
// carried in Call.Metadata. Consumers implement this interface to encode
// their own tuning heuristics (emotion-based, personality-based, etc.).
type ParameterTuner interface {
	// Tune returns the parameters to use for a single LLM call.
	// metadata is Call.Metadata — the consumer may type-assert its own
	// keys. Implementations must be safe for concurrent use.
	Tune(ctx context.Context, base TuningParams, metadata map[string]any) (TuningParams, error)
}

// TokenBudgetManager allocates and tracks the context window budget.
type TokenBudgetManager interface {
	// Allocate returns a budget for the given messages and tools.
	Allocate(ctx context.Context, msgs []Message, tools []ToolDef, cfg BudgetConfig) (*Budget, error)
}

// Budget is the token allocation result.
type Budget struct {
	TotalBudget int // total tokens allocated
	Used        int // tokens consumed
	Available   int
}

// BudgetConfig controls budget allocation.
type BudgetConfig struct {
	MaxContextTokens int
	MaxResponseTokens int
	ReserveForTools   int
}

// ToolDef is a minimal tool descriptor for budget estimation.
type ToolDef struct {
	Name        string
	Description string
}

// RetryStrategy decides whether and when to retry a failed LLM call.
type RetryStrategy interface {
	// ShouldRetry returns the delay before the next attempt and whether
	// retrying is worthwhile. attempt is 1-based.
	ShouldRetry(attempt int, err error) (delay time.Duration, ok bool)

	// CircuitBreaker returns the current breaker state.
	CircuitBreaker() BreakerState
}

// BreakerState describes the circuit breaker status.
type BreakerState int

const (
	BreakerClosed   BreakerState = iota // normal operation
	BreakerOpen                         // rejecting requests
	BreakerHalfOpen                     // testing recovery
)

// MetricsCollector records and aggregates LLM call metrics.
type MetricsCollector interface {
	Record(ctx context.Context, m CallMetrics) error
	Snapshot(ctx context.Context) (*MetricsSnapshot, error)
	Reset(ctx context.Context) error
}

// CallMetrics is a single call's metrics payload.
type CallMetrics struct {
	Duration    time.Duration
	Tokens      TokenUsage
	Model       string
	Provider    string
	Success     bool
	RetryCount  int
	Error       string // empty on success
}

// MetricsSnapshot is an aggregated view of all recorded metrics.
type MetricsSnapshot struct {
	TotalCalls    int64
	TotalTokens   int64
	AvgLatency    time.Duration
	P95Latency    time.Duration
	ErrorRate     float64
	TotalErrors   int64
	ByModel       map[string]ModelMetrics
	ByProvider    map[string]ProviderMetrics
}

// ModelMetrics aggregates metrics per model.
type ModelMetrics struct {
	Calls       int64
	TotalTokens int64
	AvgLatency  time.Duration
	Errors      int64
}

// ProviderMetrics aggregates metrics per provider.
type ProviderMetrics struct {
	Calls       int64
	AvgLatency  time.Duration
	ErrorRate   float64
}

// PromptEngine renders named templates into system prompts.
type PromptEngine interface {
	// Render fills a named template with data and returns the result.
	Render(name string, data any) (string, error)

	// Register adds or replaces a named template.
	Register(name, template string) error
}

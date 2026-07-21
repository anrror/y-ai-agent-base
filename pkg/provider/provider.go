// Package provider defines interfaces for external service providers
// (LLM, embedding, safety guards).
package provider

import (
	"context"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ProviderStatus represents the current health state of a provider.
type ProviderStatus int

const (
	// StatusHealthy means the provider is operating normally.
	StatusHealthy ProviderStatus = iota
	// StatusDegraded means the circuit breaker is open — the provider has
	// exceeded the consecutive failure threshold and calls are rejected.
	StatusDegraded
	// StatusUnavailable means the provider is permanently unreachable
	// (e.g. misconfigured base URL or invalid credentials).
	StatusUnavailable
)

// ProviderHealth describes the current health state of a provider.
type ProviderHealth struct {
	Status              ProviderStatus
	Latency             time.Duration
	LastError           string
	ConsecutiveFailures int
	LastChecked         time.Time
}

// LLMProvider is the interface for language model providers.
type LLMProvider interface {
	// Chat sends a conversation and returns the full response text.
	Chat(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error)

	// ChatStream sends a conversation and returns a channel of streamed events.
	// The channel is closed when the stream completes (or errors).
	ChatStream(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error)

	// Ping checks whether the provider is reachable. Implementations should
	// use a lightweight endpoint and a short timeout.
	Ping(ctx context.Context) error
}

// EmbeddingProvider generates vector embeddings for text.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// GuardProvider checks content against safety policies.
// Returns true when the content is allowed.
type GuardProvider interface {
	Check(ctx context.Context, text string) (bool, error)
}

// Provider exposes metadata and lifecycle methods shared by all providers.
type Provider interface {
	Name() string
	Models() []string
	Ping(ctx context.Context) error
	Close() error
}

// CompositeProvider wraps multiple specialized providers and exposes
// each role independently.
type CompositeProvider interface {
	LLMProvider
	EmbeddingProvider
	GuardProvider
	Provider
	LLM() LLMProvider
	Embedding() EmbeddingProvider
	SafetyGuard() GuardProvider
}

// ProviderConfig holds connection parameters for a single provider instance.
// This is a plain struct — no mapstructure/json tags (not for serialization).
type ProviderConfig struct {
	Type    string
	APIKey  string
	BaseURL string
	Model   string
}

// ProviderSet holds optional providers for each role.
// A nil field means the capability is disabled.
type ProviderSet struct {
	Chat      LLMProvider
	Embedding EmbeddingProvider
	Guard     GuardProvider
}

// Package inference provides pluggable model inference routing with
// capability-based endpoint resolution.
package inference

import (
	"context"
	"time"
)

// Capability identifies the kind of model capability needed.
type Capability string

const (
	CapChat   Capability = "chat"
	CapEmbed  Capability = "embed"
	CapGuard  Capability = "guard"
	CapReason Capability = "reason"
)

// HealthStatus describes the current health of a provider endpoint.
type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthHealthy
	HealthDegraded
	HealthUnhealthy
)

// ProviderInfo describes a registered provider's capabilities and endpoint.
type ProviderInfo struct {
	Name         string
	Model        string
	Capabilities []Capability
	BaseURL      string
	APIKey       string // populated from config, not serialised
	Priority     int
	Health       HealthStatus
}

// Endpoint is the resolved target for a specific capability.
type Endpoint struct {
	ProviderName string
	Model        string
	Capability   Capability
	BaseURL      string
	Health       HealthStatus
}

// Request describes what the caller needs to resolve.
type Request struct {
	Model      string     // preferred model name (empty = any)
	Capability Capability // required capability
	Timeout    time.Duration
}

// Router resolves provider endpoints by capability, with health-aware
// fallback and priority ordering.
type Router interface {
	// Resolve returns the best matching endpoint for the request.
	Resolve(ctx context.Context, req *Request) (*Endpoint, error)

	// Register adds or updates a provider in the routing table.
	Register(ctx context.Context, info ProviderInfo) error

	// Unregister removes a specific model from a provider.
	Unregister(ctx context.Context, name, model string) error

	// Health returns the current health status of all registered providers.
	Health(ctx context.Context) map[string]HealthStatus
}

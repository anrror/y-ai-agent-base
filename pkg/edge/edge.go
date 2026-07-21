// Package edge provides pluggable edge-cloud co-ordination with four
// operating modes: CloudOnly, EdgeAssist, EdgeOnly, Hybrid.
package edge

import (
	"context"
	"time"
)

// Mode identifies the edge-cloud strategy.
type Mode string

const (
	ModeCloudOnly  Mode = "cloud_only"
	ModeEdgeAssist Mode = "edge_assist"
	ModeEdgeOnly   Mode = "edge_only"
	ModeHybrid     Mode = "hybrid"
)

// Config controls edge behaviour.
type Config struct {
	Mode             Mode
	EdgeEndpoint     string
	CloudEndpoint    string
	SyncInterval     time.Duration // how often edge syncs state to cloud
	FallbackMode     Mode          // fallback when edge is unreachable
	OfflineCacheSize int
}

// Request describes the workload to be routed.
type Request struct {
	UserID         string
	SessionID      string
	ModelConfig    map[string]any
	EstimatedTokens int
	LatencySensitive bool
}

// Decision is the routing result.
type Decision struct {
	Mode         Mode
	Endpoint     string
	UseEdgeCache bool
	SyncRequired bool
}

// Manager decides whether a request should be handled locally (edge) or
// by the cloud, based on the configured mode and runtime conditions.
type Manager interface {
	// Decide returns the routing decision for a single request.
	Decide(ctx context.Context, req *Request) (*Decision, error)

	// Mode returns the currently active operating mode.
	Mode() Mode

	// Config returns a copy of the current configuration.
	Config() Config
}

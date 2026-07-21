// Package scheduler provides pluggable multi-model endpoint scheduling
// strategies: latency, cost, balanced, and priority-based.
package scheduler

import (
	"context"
	"time"
)

// Strategy identifies the scheduling algorithm.
type Strategy string

const (
	StrategyLatency  Strategy = "latency"
	StrategyCost     Strategy = "cost"
	StrategyBalanced Strategy = "balanced"
	StrategyPriority Strategy = "priority"
)

// ProviderProfile describes a model endpoint for scheduling decisions.
type ProviderProfile struct {
	Name     string
	Model    string
	Latency  time.Duration // P50 response time
	Cost     float64       // USD per 1K tokens
	Priority int           // higher = more preferred
	Capacity int           // remaining capacity slots (-1 = unlimited)
}

// Request describes what the caller needs.
type Request struct {
	RequiredCapabilities []string
	MaxLatency           time.Duration // 0 = no constraint
	MaxCost              float64       // 0 = no constraint
	PreferredModel       string
}

// Decision is the scheduling result.
type Decision struct {
	Selected *ProviderProfile
	Score    float64
	Fallback bool // true when the selected provider is a degraded choice
}

// Scheduler selects the best provider from a set of candidates.
type Scheduler interface {
	Schedule(ctx context.Context, request *Request, providers []ProviderProfile) (*Decision, error)
	Strategy() Strategy
}

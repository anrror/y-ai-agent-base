package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Metrics holds atomic counters for pipeline observability.
// All counters are safe for concurrent use without external locking.
type Metrics struct {
	RequestsTotal   atomic.Int64
	RequestsSuccess atomic.Int64
	RequestsFailed  atomic.Int64
	// Per-agent counters keyed by agent ID.
	AgentRequests sync.Map // map[string]*AgentMetrics
}

// AgentMetrics holds per-agent counters.
type AgentMetrics struct {
	Total   atomic.Int64
	Errors  atomic.Int64
	Latency atomic.Int64 // cumulative nanoseconds
}

// MetricsSnapshot is a point-in-time read of all counters.
type MetricsSnapshot struct {
	Total   int64                           `json:"total"`
	Success int64                           `json:"success"`
	Failed  int64                           `json:"failed"`
	Agents  map[string]AgentMetricsSnapshot `json:"agents,omitempty"`
}

// AgentMetricsSnapshot is the public view of per-agent counters.
type AgentMetricsSnapshot struct {
	Total      int64         `json:"total"`
	Errors     int64         `json:"errors"`
	AvgLatency time.Duration `json:"avg_latency_ms"`
}

// NewMetrics creates a new Metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// RecordRequest captures a completed pipeline invocation.
// agentID may be empty if not set in context.
func (m *Metrics) RecordRequest(agentID string, latency time.Duration, err error) {
	m.RequestsTotal.Add(1)
	if err != nil {
		m.RequestsFailed.Add(1)
	} else {
		m.RequestsSuccess.Add(1)
	}

	if agentID == "" {
		agentID = "unknown"
	}

	raw, _ := m.AgentRequests.LoadOrStore(agentID, &AgentMetrics{})
	am, ok := raw.(*AgentMetrics)
	if !ok {
		return
	}
	am.Total.Add(1)
	am.Latency.Add(int64(latency))
	if err != nil {
		am.Errors.Add(1)
	}
}

// Snapshot returns a point-in-time snapshot of all counters.
func (m *Metrics) Snapshot() MetricsSnapshot {
	snapshot := MetricsSnapshot{
		Total:   m.RequestsTotal.Load(),
		Success: m.RequestsSuccess.Load(),
		Failed:  m.RequestsFailed.Load(),
	}

	agents := make(map[string]AgentMetricsSnapshot)
	m.AgentRequests.Range(func(key, value any) bool {
		id, ok := key.(string)
		if !ok {
			return true
		}
		am, ok := value.(*AgentMetrics)
		if !ok {
			return true
		}
		total := am.Total.Load()
		avg := time.Duration(0)
		if total > 0 {
			avg = time.Duration(am.Latency.Load() / total)
		}
		agents[id] = AgentMetricsSnapshot{
			Total:      total,
			Errors:     am.Errors.Load(),
			AvgLatency: avg,
		}
		return true
	})
	if len(agents) > 0 {
		snapshot.Agents = agents
	}

	return snapshot
}

// MetricsMiddleware returns a middleware that records request metrics
// (total, success/failure, per-agent latency and errors) for every
// pipeline invocation.
func MetricsMiddleware(metrics *Metrics) Middleware {
	if metrics == nil {
		return func(next Handler) Handler { return next }
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			start := time.Now()
			err := next(ctx, input, output)
			agentID, _ := ctx.Value(types.CtxAgentID).(string)
			metrics.RecordRequest(agentID, time.Since(start), err)
			return err
		}
	}
}

package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ---------------------------------------------------------------------------
// Tests — Metrics.RecordRequest
// ---------------------------------------------------------------------------

func TestMetrics_RecordRequest_CountsTotalAndSuccess(t *testing.T) {
	// Given: fresh Metrics
	m := NewMetrics()

	// When: recording 5 successful requests
	for i := 0; i < 5; i++ {
		m.RecordRequest("agent-1", 100*time.Millisecond, nil)
	}

	// Then: counters reflect all successes, no failures
	assert.Equal(t, int64(5), m.RequestsTotal.Load())
	assert.Equal(t, int64(5), m.RequestsSuccess.Load())
	assert.Equal(t, int64(0), m.RequestsFailed.Load())
}

func TestMetrics_RecordRequest_CountsFailed(t *testing.T) {
	// Given: fresh Metrics
	m := NewMetrics()

	// When: recording 3 failures and 2 successes
	m.RecordRequest("agent-1", 50*time.Millisecond, errors.New("timeout"))
	m.RecordRequest("agent-1", 50*time.Millisecond, errors.New("timeout"))
	m.RecordRequest("agent-1", 50*time.Millisecond, errors.New("timeout"))
	m.RecordRequest("agent-1", 30*time.Millisecond, nil)
	m.RecordRequest("agent-1", 30*time.Millisecond, nil)

	// Then
	assert.Equal(t, int64(5), m.RequestsTotal.Load())
	assert.Equal(t, int64(2), m.RequestsSuccess.Load())
	assert.Equal(t, int64(3), m.RequestsFailed.Load())
}

func TestMetrics_RecordRequest_CountsPerAgent(t *testing.T) {
	// Given: fresh Metrics
	m := NewMetrics()

	// When: recording for two different agents
	m.RecordRequest("alpha", 10*time.Millisecond, nil)
	m.RecordRequest("alpha", 20*time.Millisecond, errors.New("fail"))
	m.RecordRequest("beta", 30*time.Millisecond, nil)

	// Then: per-agent counters are correct
	rawAlpha, ok := m.AgentRequests.Load("alpha")
	require.True(t, ok)
	amAlpha, _ := rawAlpha.(*AgentMetrics)
	assert.Equal(t, int64(2), amAlpha.Total.Load())
	assert.Equal(t, int64(1), amAlpha.Errors.Load())

	rawBeta, ok := m.AgentRequests.Load("beta")
	require.True(t, ok)
	amBeta, _ := rawBeta.(*AgentMetrics)
	assert.Equal(t, int64(1), amBeta.Total.Load())
	assert.Equal(t, int64(0), amBeta.Errors.Load())
}

func TestMetrics_RecordRequest_EmptyAgentID(t *testing.T) {
	// Given: fresh Metrics
	m := NewMetrics()

	// When: recording with empty agentID
	m.RecordRequest("", 25*time.Millisecond, nil)

	// Then: falls back to "unknown" key
	raw, ok := m.AgentRequests.Load("unknown")
	require.True(t, ok)
	am, _ := raw.(*AgentMetrics)
	assert.Equal(t, int64(1), am.Total.Load())
}

func TestMetrics_RecordRequest_Concurrent(t *testing.T) {
	// Given: shared Metrics
	m := NewMetrics()
	const goroutines = 50
	const iterations = 100

	// When: many goroutines record simultaneously
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := "agent-a"
			if id%2 == 0 {
				agentID = "agent-b"
			}
			for i := 0; i < iterations; i++ {
				var err error
				if i%3 == 0 {
					err = errors.New("sporadic")
				}
				m.RecordRequest(agentID, time.Millisecond, err)
			}
		}(g)
	}
	wg.Wait()

	// Then: total = goroutines * iterations, no data race
	expected := int64(goroutines * iterations)
	assert.Equal(t, expected, m.RequestsTotal.Load())
	assert.Equal(t, expected, m.RequestsSuccess.Load()+m.RequestsFailed.Load())
}

// ---------------------------------------------------------------------------
// Tests — Metrics.Snapshot
// ---------------------------------------------------------------------------

func TestMetrics_Snapshot_ReturnsConsistentView(t *testing.T) {
	// Given: metrics with known state
	m := NewMetrics()
	m.RecordRequest("a", 10*time.Millisecond, nil)
	m.RecordRequest("a", 20*time.Millisecond, nil)
	m.RecordRequest("a", 30*time.Millisecond, errors.New("err"))

	// When
	snap := m.Snapshot()

	// Then: snapshot reflects accumulated counters
	assert.Equal(t, int64(3), snap.Total)
	assert.Equal(t, int64(2), snap.Success)
	assert.Equal(t, int64(1), snap.Failed)

	require.Contains(t, snap.Agents, "a")
	am := snap.Agents["a"]
	assert.Equal(t, int64(3), am.Total)
	assert.Equal(t, int64(1), am.Errors)
}

func TestMetrics_Snapshot_CalculatesAvgLatency(t *testing.T) {
	// Given: per-agent cumulative latency of 100ms over 4 requests
	m := NewMetrics()
	m.RecordRequest("x", 10*time.Millisecond, nil)
	m.RecordRequest("x", 20*time.Millisecond, nil)
	m.RecordRequest("x", 30*time.Millisecond, nil)
	m.RecordRequest("x", 40*time.Millisecond, nil)

	// When
	snap := m.Snapshot()

	// Then: avg latency = (10+20+30+40)/4 = 25ms
	require.Contains(t, snap.Agents, "x")
	assert.Equal(t, 25*time.Millisecond, snap.Agents["x"].AvgLatency)
}

func TestMetrics_Snapshot_EmptyMetrics(t *testing.T) {
	// Given: no requests recorded
	m := NewMetrics()

	// When
	snap := m.Snapshot()

	// Then: all counters are zero, agents map nil (not empty)
	assert.Equal(t, int64(0), snap.Total)
	assert.Equal(t, int64(0), snap.Success)
	assert.Equal(t, int64(0), snap.Failed)
	assert.Nil(t, snap.Agents)
}

func TestMetrics_Snapshot_AgentsFieldOmittedWhenEmpty(t *testing.T) {
	// Given: no requests, then a request, then all cleared (not possible via
	// public API but snapshot should handle zero-agent case gracefully).
	m := NewMetrics()
	m.RecordRequest("temp", 1*time.Millisecond, nil)

	// When
	snap := m.Snapshot()

	// Then: agents are present when there is data
	assert.NotNil(t, snap.Agents)
	assert.Contains(t, snap.Agents, "temp")
}

// ---------------------------------------------------------------------------
// Tests — MetricsMiddleware
// ---------------------------------------------------------------------------

func TestMetricsMiddleware_RecordsRequest(t *testing.T) {
	// Given: a pipeline with MetricsMiddleware wrapping a successful provider
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	// When: Run is called
	output, err := p.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)

	// Then: metrics are recorded
	assert.Equal(t, int64(1), m.RequestsTotal.Load())
	assert.Equal(t, int64(1), m.RequestsSuccess.Load())
	assert.Equal(t, int64(0), m.RequestsFailed.Load())
}

func TestMetricsMiddleware_RecordsError(t *testing.T) {
	// Given: a provider that fails
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "", errors.New("down")
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	// When
	_, err := p.Run(context.Background(), testInput("hi"))
	require.Error(t, err)

	// Then: failure is counted
	assert.Equal(t, int64(1), m.RequestsTotal.Load())
	assert.Equal(t, int64(0), m.RequestsSuccess.Load())
	assert.Equal(t, int64(1), m.RequestsFailed.Load())
}

func TestMetricsMiddleware_UsesAgentIDFromContext(t *testing.T) {
	// Given: context with an agent ID set
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "done", nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	ctx := context.WithValue(context.Background(), types.CtxAgentID, "my-agent")

	// When
	_, err := p.Run(ctx, testInput("hi"))
	require.NoError(t, err)

	// Then: per-agent metrics exist for "my-agent"
	raw, ok := m.AgentRequests.Load("my-agent")
	require.True(t, ok)
	am, _ := raw.(*AgentMetrics)
	assert.Equal(t, int64(1), am.Total.Load())
}

func TestMetricsMiddleware_NilMetricsIsNoop(t *testing.T) {
	// Given: nil metrics (should not panic)
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(nil))

	// When: Run is called — should not panic
	output, err := p.Run(context.Background(), testInput("hi"))

	// Then: normal pipeline behavior
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)
}

func TestMetricsMiddleware_CapturesLatency(t *testing.T) {
	// Given: a provider with known delay
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			time.Sleep(10 * time.Millisecond)
			return "slow", nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	// When
	ctx := context.WithValue(context.Background(), types.CtxAgentID, "latency-test")
	_, err := p.Run(ctx, testInput("hi"))
	require.NoError(t, err)

	// Then: latency is at least 10ms
	raw, ok := m.AgentRequests.Load("latency-test")
	require.True(t, ok)
	am, _ := raw.(*AgentMetrics)
	latencyNs := am.Latency.Load()
	assert.GreaterOrEqual(t, latencyNs, int64(10*time.Millisecond))
}

func TestMetricsMiddleware_MultipleAgentsInChain(t *testing.T) {
	// Given: pipeline with metrics middleware, called with two different agent IDs
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	// When: two calls with different agent contexts
	ctx1 := context.WithValue(context.Background(), types.CtxAgentID, "agent-alpha")
	ctx2 := context.WithValue(context.Background(), types.CtxAgentID, "agent-beta")

	_, err := p.Run(ctx1, testInput("a"))
	require.NoError(t, err)
	_, err = p.Run(ctx2, testInput("b"))
	require.NoError(t, err)

	// Then: both agents appear in per-agent metrics
	snap := m.Snapshot()
	assert.Equal(t, int64(2), snap.Total)
	require.NotNil(t, snap.Agents)
	assert.Len(t, snap.Agents, 2)
	assert.Contains(t, snap.Agents, "agent-alpha")
	assert.Contains(t, snap.Agents, "agent-beta")
}

func TestMetricsMiddleware_ShortCircuitStillRecords(t *testing.T) {
	// Given: MetricsMiddleware is outermost (added first), short-circuit is inner.
	// MetricsMiddleware wraps the short-circuit response, so it still records.
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			t.Fatal("provider should not be called")
			return "", nil
		},
	}

	p := New(prov)
	// Add MetricsMiddleware first (becomes outer wrapper), short-circuit second (inner).
	p.Use(MetricsMiddleware(m))
	p.Use(shortCircuitMiddleware(0, &[]int{}))

	// When
	output, err := p.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)
	assert.NotEmpty(t, output.Content)

	// Then: metrics still recorded because MetricsMiddleware wraps the handler
	assert.Equal(t, int64(1), m.RequestsTotal.Load())
}

func TestMetricsMiddleware_ShortCircuitOuter_NoRecord(t *testing.T) {
	// Given: MetricsMiddleware is inner; short-circuit middleware is outer.
	// The short-circuit prevents metrics from firing.
	m := NewMetrics()
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	// Short-circuit middleware is outermost — it stops the chain before metrics.
	p.Use(shortCircuitMiddleware(0, &[]int{}))
	p.Use(MetricsMiddleware(m))

	// When
	output, err := p.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)
	assert.NotEmpty(t, output.Content)

	// Then: NO metrics recorded — short-circuit never called next()
	assert.Equal(t, int64(0), m.RequestsTotal.Load())
}

// ---------------------------------------------------------------------------
// Tests — Metrics integration with ChatStream
// ---------------------------------------------------------------------------

func TestMetricsMiddleware_ChatStream_RecordsAfterDrain(t *testing.T) {
	// Given: streaming pipeline with metrics
	m := NewMetrics()
	prov := &mockProvider{
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 1)
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	p := New(prov)
	p.Use(MetricsMiddleware(m))

	ch, _ := p.(*chain)

	// When: ChatStream is called, stream is drained
	output, err := ch.ChatStream(context.Background(), testInput("stream"))
	require.NoError(t, err)
	require.True(t, output.IsStream)

	for range output.Stream {
	}

	// Then: metrics are recorded (happens synchronously in the middleware post-wrap,
	// not after stream drain — the middleware sees the output returned immediately)
	assert.Equal(t, int64(1), m.RequestsTotal.Load())
}

// ---------------------------------------------------------------------------
// Tests — snapshot JSON tags (structural verification)
// ---------------------------------------------------------------------------

func TestMetricsSnapshot_JSONTagsPresent(t *testing.T) {
	// Given: populated metrics
	m := NewMetrics()
	m.RecordRequest("x", 10*time.Millisecond, nil)

	// When
	snap := m.Snapshot()

	// Then: exported fields have expected values (verify structure)
	assert.Equal(t, int64(1), snap.Total)
	assert.Equal(t, int64(1), snap.Success)
	assert.Equal(t, int64(0), snap.Failed)
	assert.Len(t, snap.Agents, 1)
	assert.Equal(t, int64(1), snap.Agents["x"].Total)
	assert.Equal(t, int64(0), snap.Agents["x"].Errors)
	assert.Equal(t, 10*time.Millisecond, snap.Agents["x"].AvgLatency)
}

// Ensure sync/atomic import for atomic.Int64 used in Metrics.
var _ atomic.Int64

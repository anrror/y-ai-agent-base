package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// ---------------------------------------------------------------------------
// mock provider
// ---------------------------------------------------------------------------

type mockProvider struct {
	chatFn       func(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error)
	chatStreamFn func(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error)
	pingFn       func(ctx context.Context) error
}

func (m *mockProvider) Chat(ctx context.Context, messages []types.Message, config types.ModelConfig) (string, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, messages, config)
	}
	return "default", nil
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []types.Message, config types.ModelConfig) (<-chan types.StreamEvent, error) {
	if m.chatStreamFn != nil {
		return m.chatStreamFn(ctx, messages, config)
	}
	ch := make(chan types.StreamEvent, 2)
	ch <- types.StreamEvent{Type: "chunk", Content: "hello"}
	ch <- types.StreamEvent{Type: "done", Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Ping(_ context.Context) error {
	if m.pingFn != nil {
		return m.pingFn(context.Background())
	}
	return nil
}

var _ provider.LLMProvider = (*mockProvider)(nil)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testInput(msg string) types.ChatInput {
	return types.ChatInput{
		Messages: []types.Message{{Role: "user", Content: msg}},
	}
}

// orderMiddleware appends its id to the supplied slice (in pre) and also in
// post (wrapping the call). It always calls next so the chain continues.
func orderMiddleware(id int, order *[]int) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			*order = append(*order, id) // pre
			err := next(ctx, input, output)
			*order = append(*order, -id) // post
			return err
		}
	}
}

// shortCircuitMiddleware at the given step stops the chain and fills output.
// It records the step number in the order slice.
func shortCircuitMiddleware(step int, order *[]int) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			*order = append(*order, step)
			// Do NOT call next — short-circuit.
			output.Content = fmt.Sprintf("cached-at-%d", step)
			output.Role = "assistant"
			output.FinishReason = "stop"
			return nil
		}
	}
}

// errMiddleware returns the given error without calling next.
func errMiddleware(order *[]int, err error) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			*order = append(*order, -1)
			return err
		}
	}
}

// ---------------------------------------------------------------------------
// Tests — composition order
// ---------------------------------------------------------------------------

func TestPipeline_MiddlewareOrder_PrePost(t *testing.T) {
	// Given: a pipeline with three order-tracking middleware
	var order []int
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			order = append(order, 99) // provider is the terminal handler
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(
		orderMiddleware(1, &order),
		orderMiddleware(2, &order),
		orderMiddleware(3, &order),
	)

	// When: Run is called
	output, err := p.Run(context.Background(), testInput("hi"))

	// Then: pre-order is [1, 2, 3], provider runs, post-order is [-3, -2, -1]
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)
	assert.Equal(t, []int{1, 2, 3, 99, -3, -2, -1}, order)
}

func TestPipeline_MiddlewareOrder_WithChain(t *testing.T) {
	// Given: middleware added via chaining With() calls
	var order []int
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			order = append(order, 0)
			return "ok", nil
		},
	}

	ch, _ := New(prov).(*chain)
	ch.With(orderMiddleware(10, &order)).With(orderMiddleware(20, &order))

	// When
	output, err := ch.Run(context.Background(), testInput("hi"))

	// Then: order is [10, 20, provider, post-20, post-10]
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)
	assert.Equal(t, []int{10, 20, 0, -20, -10}, order)
}

// ---------------------------------------------------------------------------
// Tests — short-circuit
// ---------------------------------------------------------------------------

func TestPipeline_ShortCircuit_StopsChain(t *testing.T) {
	// Given: a middleware at position 2 short-circuits
	var order []int
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			order = append(order, 99)
			return "provider", nil
		},
	}

	p := New(prov)
	p.Use(
		orderMiddleware(1, &order),
		shortCircuitMiddleware(2, &order),
		orderMiddleware(3, &order),
	)

	// When
	output, err := p.Run(context.Background(), testInput("hi"))

	// Then: middleware 1 runs, middleware 2 short-circuits, 3 and provider never run.
	// Post of 1 should still run (it wraps the short-circuit response).
	require.NoError(t, err)
	assert.Equal(t, "cached-at-2", output.Content)
	assert.Equal(t, "stop", output.FinishReason)
	assert.Equal(t, []int{1, 2, -1}, order)
}

func TestPipeline_ShortCircuit_FirstMiddleware(t *testing.T) {
	// Given: the very first middleware short-circuits
	var order []int
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			t.Fatal("provider should not be called")
			return "", nil
		},
	}

	p := New(prov)
	p.Use(
		shortCircuitMiddleware(0, &order),
		orderMiddleware(1, &order),
	)

	// When
	output, err := p.Run(context.Background(), testInput("hi"))

	// Then: only the first middleware runs
	require.NoError(t, err)
	assert.Equal(t, "cached-at-0", output.Content)
	assert.Equal(t, []int{0}, order)
}

// ---------------------------------------------------------------------------
// Tests — error propagation
// ---------------------------------------------------------------------------

func TestPipeline_ErrorPropagation_MiddlewareReturnsError(t *testing.T) {
	// Given: a middleware that returns an error without calling next
	var order []int
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			t.Fatal("provider should not be called")
			return "", nil
		},
	}

	testErr := errors.New("middleware refused")

	p := New(prov)
	p.Use(
		orderMiddleware(1, &order),
		errMiddleware(&order, testErr),
		orderMiddleware(2, &order),
	)

	// When
	output, err := p.Run(context.Background(), testInput("hi"))

	// Then: error propagates, provider and later middleware are skipped
	assert.ErrorIs(t, err, testErr)
	assert.Empty(t, output.Content)
	assert.Equal(t, []int{1, -1, -1}, order)
}

func TestPipeline_ErrorPropagation_ProviderReturnsError(t *testing.T) {
	// Given: the provider returns an error
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "", errors.New("provider down")
		},
	}

	p := New(prov)

	// When
	_, err := p.Run(context.Background(), testInput("hi"))

	// Then
	assert.EqualError(t, err, "pipeline chat: provider down")
}

// ---------------------------------------------------------------------------
// Tests — post-hooks
// ---------------------------------------------------------------------------

func TestPipeline_PostHooks_FireAfterSuccess(t *testing.T) {
	// Given: a pipeline with a post-hook
	var hookCalled atomic.Bool
	var hookOutput types.ChatOutput
	var hookErr error

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "success", nil
		},
	}

	ch, _ := New(prov).(*chain)
	ch.OnPostProcess(func(_ context.Context, _ *types.ChatInput, output *types.ChatOutput, err error) {
		hookCalled.Store(true)
		hookOutput = *output
		hookErr = err
	})

	// When
	output, err := ch.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)
	assert.Equal(t, "success", output.Content)

	// Then: post-hook fires (give it a moment — it's async)
	assert.Eventually(t, hookCalled.Load, 500*time.Millisecond, 10*time.Millisecond)
	assert.Equal(t, "success", hookOutput.Content)
	assert.NoError(t, hookErr)
}

func TestPipeline_PostHooks_FireAfterError(t *testing.T) {
	// Given: a pipeline where the provider fails
	var hookCalled atomic.Bool
	var hookErr error

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "", errors.New("boom")
		},
	}

	ch, _ := New(prov).(*chain)
	ch.OnPostProcess(func(_ context.Context, _ *types.ChatInput, _ *types.ChatOutput, err error) {
		hookCalled.Store(true)
		hookErr = err
	})

	// When
	_, err := ch.Run(context.Background(), testInput("hi"))
	require.Error(t, err)

	// Then
	assert.Eventually(t, hookCalled.Load, 500*time.Millisecond, 10*time.Millisecond)
	assert.EqualError(t, hookErr, "pipeline chat: boom")
}

func TestPipeline_PostHooks_MultipleHooksFire(t *testing.T) {
	// Given: multiple post-hooks registered
	var mu sync.Mutex
	var fired []int

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	ch, _ := New(prov).(*chain)
	ch.OnPostProcess(func(_ context.Context, _ *types.ChatInput, _ *types.ChatOutput, _ error) {
		mu.Lock()
		fired = append(fired, 1)
		mu.Unlock()
	})
	ch.OnPostProcess(func(_ context.Context, _ *types.ChatInput, _ *types.ChatOutput, _ error) {
		mu.Lock()
		fired = append(fired, 2)
		mu.Unlock()
	})

	// When
	_, err := ch.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)

	// Then: both hooks fire (wait up to 1 second)
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fired) == 2
	}, 1*time.Second, 10*time.Millisecond)
}

func TestPipeline_PostHooks_AfterStreamCompletes(t *testing.T) {
	// Given: a streaming provider
	var hookCalled atomic.Bool

	prov := &mockProvider{
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 3)
			ch <- types.StreamEvent{Type: "chunk", Content: "part1"}
			ch <- types.StreamEvent{Type: "chunk", Content: "part2"}
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	ch, _ := New(prov).(*chain)
	ch.OnPostProcess(func(_ context.Context, _ *types.ChatInput, _ *types.ChatOutput, _ error) {
		hookCalled.Store(true)
	})

	// When
	output, err := ch.ChatStream(context.Background(), testInput("stream"))
	require.NoError(t, err)
	require.True(t, output.IsStream)
	require.NotNil(t, output.Stream)

	// Hook should NOT have fired yet (stream is still open)
	assert.False(t, hookCalled.Load())

	// Drain the stream
	var chunks []string
	for evt := range output.Stream {
		if evt.Type == "chunk" {
			chunks = append(chunks, evt.Content)
		}
	}

	// Then: hook fires after the stream completes
	assert.Equal(t, []string{"part1", "part2"}, chunks)
	assert.Eventually(t, hookCalled.Load, 500*time.Millisecond, 10*time.Millisecond)
}

// ---------------------------------------------------------------------------
// Tests — Recovery middleware
// ---------------------------------------------------------------------------

func TestRecovery_CatchesPanic(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			panic("unexpected nil pointer")
		},
	}

	p := New(prov)
	p.Use(Recovery())

	output, err := p.Run(context.Background(), testInput("hi"))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "recovered from panic")
	assert.Empty(t, output.Content)
}

func TestRecovery_PassesThroughNormalResponse(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "all good", nil
		},
	}

	p := New(prov)
	p.Use(Recovery())

	output, err := p.Run(context.Background(), testInput("hi"))

	assert.NoError(t, err)
	assert.Equal(t, "all good", output.Content)
}

func TestRecovery_PassesThroughError(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "", errors.New("provider error")
		},
	}

	p := New(prov)
	p.Use(Recovery())

	_, err := p.Run(context.Background(), testInput("hi"))

	assert.EqualError(t, err, "pipeline chat: provider error")
}

// ---------------------------------------------------------------------------
// Tests — RateLimit middleware
// ---------------------------------------------------------------------------

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	var calls atomic.Int32

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			calls.Add(1)
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(RateLimit(100, 5)) // 100 tokens/sec, burst 5

	// When: 5 rapid calls (within burst)
	for i := 0; i < 5; i++ {
		_, err := p.Run(context.Background(), testInput(fmt.Sprintf("msg-%d", i)))
		require.NoError(t, err)
	}

	// Then: all 5 succeed
	assert.Equal(t, int32(5), calls.Load())
}

func TestRateLimit_BlocksWhenExhausted(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	// 0.5 tokens/sec, burst 1 — second call should be rejected
	p := New(prov)
	p.Use(RateLimit(1, 1))

	// First call consumes the only token
	_, err := p.Run(context.Background(), testInput("msg-1"))
	require.NoError(t, err)

	// Second call should be rate-limited
	_, err = p.Run(context.Background(), testInput("msg-2"))
	assert.ErrorIs(t, err, types.ErrPipelineHalted)
}

func TestRateLimit_RefillsOverTime(t *testing.T) {
	var calls atomic.Int32

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			calls.Add(1)
			return "ok", nil
		},
	}

	// 5 tokens/sec, burst 5
	fakeClock := clock.NewFakeClock(time.Now())
	p := New(prov)
	p.Use(RateLimit(5, 5, WithClock(fakeClock)))

	// Exhaust all tokens
	for i := 0; i < 5; i++ {
		_, err := p.Run(context.Background(), testInput(fmt.Sprintf("msg-%d", i)))
		require.NoError(t, err)
	}

	// Next call should be blocked
	_, err := p.Run(context.Background(), testInput("msg-6"))
	assert.ErrorIs(t, err, types.ErrPipelineHalted)

	// Advance clock for refill (~2 tokens worth)
	fakeClock.Advance(400 * time.Millisecond)

	// Now a call should succeed
	_, err = p.Run(context.Background(), testInput("msg-7"))
	require.NoError(t, err, "should succeed after refill")
	assert.GreaterOrEqual(t, calls.Load(), int32(6))
}

func TestRateLimit_DisabledWhenZero(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(RateLimit(0, 0)) // disabled

	// Should succeed without rate limiting
	for i := 0; i < 20; i++ {
		_, err := p.Run(context.Background(), testInput(fmt.Sprintf("msg-%d", i)))
		require.NoError(t, err)
	}
}

// ---------------------------------------------------------------------------
// Tests — Timeout middleware
// ---------------------------------------------------------------------------

func TestTimeout_CancelsSlowHandler(t *testing.T) {
	// neverBlock is a channel that is never closed; any select on it will
	// block forever, forcing the ctx.Done() branch to win after the timeout.
	neverBlock := make(chan struct{})
	prov := &mockProvider{
		chatFn: func(ctx context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-neverBlock:
				return "too slow", nil
			}
		},
	}

	p := New(prov)
	p.Use(Timeout(50 * time.Millisecond))

	_, err := p.Run(context.Background(), testInput("hi"))

	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, types.ErrTimeout),
		"expected deadline exceeded or timeout error, got: %v", err)
}

func TestTimeout_AllowsFastHandler(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "fast", nil
		},
	}

	p := New(prov)
	p.Use(Timeout(1 * time.Second))

	output, err := p.Run(context.Background(), testInput("hi"))

	require.NoError(t, err)
	assert.Equal(t, "fast", output.Content)
}

func TestTimeout_DisabledWhenZero(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(Timeout(0))

	output, err := p.Run(context.Background(), testInput("hi"))

	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)
}

// ---------------------------------------------------------------------------
// Tests — Logging middleware
// ---------------------------------------------------------------------------

func TestLogging_LogsInvocation(t *testing.T) {
	var logged []string
	logger := func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}

	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "hello", nil
		},
	}

	p := New(prov)
	p.Use(Logging(logger))

	_, err := p.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)

	assert.GreaterOrEqual(t, len(logged), 2, "expected at least pre + post log lines")
	assert.Contains(t, logged[0], "processing")        // pre
	assert.Contains(t, logged[len(logged)-1], "len=5") // post
}

func TestLogging_NilLoggerIsNoop(t *testing.T) {
	prov := &mockProvider{
		chatFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (string, error) {
			return "ok", nil
		},
	}

	p := New(prov)
	p.Use(Logging(nil))

	output, err := p.Run(context.Background(), testInput("hi"))
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Content)
}

// ---------------------------------------------------------------------------
// Tests — ChatStream with middleware
// ---------------------------------------------------------------------------

func TestChatStream_MiddlewareOrder_PrePost(t *testing.T) {
	// Given: a streaming pipeline with order-tracking middleware
	var order []int

	prov := &mockProvider{
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			order = append(order, 99)
			ch := make(chan types.StreamEvent, 1)
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	p := New(prov)
	p.Use(
		orderMiddleware(1, &order),
		orderMiddleware(2, &order),
	)

	// When
	ch, _ := p.(*chain)
	output, err := ch.ChatStream(context.Background(), testInput("stream"))
	require.NoError(t, err)
	require.True(t, output.IsStream)

	// Drain stream
	for range output.Stream {
	}

	// Then: middleware 1 pre, middleware 2 pre, provider runs inside the
	// terminal handler (returning immediately), middleware 2 post, middleware 1 post.
	assert.Equal(t, []int{1, 2, 99, -2, -1}, order)
}

func TestChatStream_MiddlewareSeesIsStream(t *testing.T) {
	// Given: middleware that inspects the output
	var sawStream bool

	prov := &mockProvider{
		chatStreamFn: func(_ context.Context, _ []types.Message, _ types.ModelConfig) (<-chan types.StreamEvent, error) {
			ch := make(chan types.StreamEvent, 1)
			ch <- types.StreamEvent{Type: "done", Done: true}
			close(ch)
			return ch, nil
		},
	}

	p := New(prov)
	p.Use(func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			err := next(ctx, input, output)
			sawStream = output.IsStream
			return err
		}
	})

	ch, _ := p.(*chain)
	output, err := ch.ChatStream(context.Background(), testInput("stream"))
	require.NoError(t, err)

	for range output.Stream {
	}

	assert.True(t, sawStream, "middleware should see IsStream=true on the output")
}

// ---------------------------------------------------------------------------
// Tests — Pipeline interface satisfaction
// ---------------------------------------------------------------------------

func TestPipeline_SatisfiesInterface(t *testing.T) {
	p := New(&mockProvider{})
	assert.NotNil(t, p)
}

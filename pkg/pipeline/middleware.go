package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Logger is the function signature used by the built-in Logging middleware.
// A nil Logger disables output.
type Logger func(format string, args ...any)

// Recovery returns middleware that recovers from panics in downstream handlers.
// When a panic is caught the chain is short-circuited and the output is kept
// zero-valued, with an error wrapping the recovered value.
func Recovery() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) (err error) {
			defer func() {
				if r := recover(); r != nil {
					*output = types.ChatOutput{}
					err = &recoveryError{recovered: r}
				}
			}()
			return next(ctx, input, output)
		}
	}
}

// Logging returns middleware that logs each pipeline invocation with the given
// Logger.  If logger is nil, logging is a no-op.
func Logging(logger Logger) Middleware {
	if logger == nil {
		return func(next Handler) Handler { return next }
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			msgCount := len(input.Messages)
			logger("pipeline: processing %d messages", msgCount)
			err := next(ctx, input, output)
			if err != nil {
				logger("pipeline: error: %v", err)
			} else if output.IsStream {
				logger("pipeline: streaming response started")
			} else {
				logger("pipeline: response len=%d finish=%s", len(output.Content), output.FinishReason)
			}
			return err
		}
	}
}

// tokenBucket is a simple in-memory token bucket rate limiter.
type tokenBucket struct {
	rate       float64
	burst      int
	tokens     float64
	lastRefill time.Time
	mu         sync.Mutex
	clock      clock.Clock
}

// getClock returns the configured clock, defaulting to RealClock if none set.
func (tb *tokenBucket) getClock() clock.Clock {
	if tb.clock == nil {
		return clock.RealClock{}
	}
	return tb.clock
}

func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := tb.getClock().Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}
	tb.lastRefill = now

	if tb.tokens >= 1.0 {
		tb.tokens--
		return true
	}
	return false
}

// RateLimit returns middleware that rate-limits pipeline invocations using an
// in-memory token bucket.  tokensPerSec controls the refill rate and burst
// controls the maximum bucket size.  When the bucket is empty the middleware
// returns ErrPipelineHalted.
//
// Optional RateLimitOptions may be passed to configure the clock (for testing).
func RateLimit(tokensPerSec int, burst int, opts ...RateLimitOption) Middleware {
	if tokensPerSec <= 0 || burst <= 0 {
		return func(next Handler) Handler { return next }
	}

	bucket := &tokenBucket{
		rate:  float64(tokensPerSec),
		burst: burst,
	}
	for _, o := range opts {
		o(bucket)
	}
	clk := bucket.getClock()
	bucket.tokens = float64(burst)
	bucket.lastRefill = clk.Now()

	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			if !bucket.allow() {
				return types.ErrPipelineHalted
			}
			return next(ctx, input, output)
		}
	}
}

// RateLimitOption configures the token bucket used by RateLimit middleware.
type RateLimitOption func(*tokenBucket)

// WithClock sets the clock implementation on the rate limiter.
func WithClock(c clock.Clock) RateLimitOption {
	return func(tb *tokenBucket) {
		tb.clock = c
	}
}

// Timeout returns middleware that enforces a per-request timeout.
// The timeout is determined by (in priority order):
//  1. ChatInput.Timeout field (if explicitly set)
//  2. ModelConfig.TimeoutSeconds (converted to duration)
//  3. Context deadline already set upstream (shorter deadline is preserved)
//  4. defaultTimeout parameter (the fallback)
//
// An existing context deadline that is shorter than the computed timeout
// is preserved as-is — we never extend a deadline.
func Timeout(defaultTimeout time.Duration) Middleware {
	if defaultTimeout <= 0 {
		return func(next Handler) Handler { return next }
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, input *types.ChatInput, output *types.ChatOutput) error {
			timeout := defaultTimeout

			if input.Timeout > 0 {
				timeout = input.Timeout
			} else if input.ModelConfig != nil && input.ModelConfig.TimeoutSeconds > 0 {
				timeout = time.Duration(input.ModelConfig.TimeoutSeconds) * time.Second
			}

			if deadline, ok := ctx.Deadline(); ok {
				if remaining := time.Until(deadline); remaining < timeout {
					timeout = remaining
				}
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return next(ctx, input, output)
		}
	}
}

// recoveryError wraps a recovered panic value as an error.
type recoveryError struct {
	recovered any
}

func (e *recoveryError) Error() string {
	return "pipeline: recovered from panic"
}

// Unwrap returns the recovered error for errors.Is/As inspection.
func (e *recoveryError) Unwrap() error {
	if err, ok := e.recovered.(error); ok {
		return err
	}
	return nil
}



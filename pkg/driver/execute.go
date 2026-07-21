package driver

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultDriver is the built-in Driver implementation that wires all
// five subsystems together.
type DefaultDriver struct {
	cfg     Config
	tuner   ParameterTuner
	budget  TokenBudgetManager
	retry   RetryStrategy
	metrics MetricsCollector
	prompts PromptEngine
}

var _ Driver = (*DefaultDriver)(nil)

// NewDefaultDriver creates a full Driver with all subsystems initialised.
// Pass nil for any subsystem to use the built-in default.
func NewDefaultDriver(cfg Config, tuner ParameterTuner, budget TokenBudgetManager, retry RetryStrategy, metrics MetricsCollector, prompts PromptEngine) *DefaultDriver {
	if tuner == nil {
		tuner = NewDefaultParameterTuner(TuningConfig{
			DefaultTemperature: 0.7,
			DefaultTopP:        0.9,
			DefaultMaxTokens:   2048,
		})
	}
	if budget == nil {
		budget = NewDefaultTokenBudgetManager(BudgetConfig{})
	}
	if retry == nil {
		retry = NewExponentialBackoff(cfg.MaxRetries, cfg.RetryBaseDelay, cfg.CircuitBreakerThreshold, cfg.CircuitBreakerCooldown)
	}
	if metrics == nil {
		metrics = NewDefaultMetricsCollector()
	}
	if prompts == nil {
		prompts = NewDefaultPromptEngine()
	}
	return &DefaultDriver{
		cfg:     cfg,
		tuner:   tuner,
		budget:  budget,
		retry:   retry,
		metrics: metrics,
		prompts: prompts,
	}
}

func (d *DefaultDriver) Configure(cfg Config) error {
	d.cfg = cfg
	return nil
}

func (d *DefaultDriver) Execute(ctx context.Context, call *Call, llmFn LLMFunc) (*Result, error) {
	start := time.Now()

	// 1. Parameter tuning — the consumer's ParameterTuner reads
	//    business context from call.Metadata (emotion, personality, etc.).
	var tuningParams TuningParams
	if d.cfg.EnableTuning && call.TuningOverride == nil {
		base := TuningParams{Temperature: 0.7, TopP: 0.9, MaxTokens: 2048}
		params, err := d.tuner.Tune(ctx, base, call.Metadata)
		if err != nil {
			return nil, fmt.Errorf("driver: tune: %w", err)
		}
		tuningParams = params
	} else if call.TuningOverride != nil {
		tuningParams = *call.TuningOverride
	} else {
		tuningParams = TuningParams{Temperature: 0.7, TopP: 0.9, MaxTokens: 2048}
	}

	// 2. Token budget allocation
	if d.cfg.EnableBudget {
		tools := make([]ToolDef, 0)
		budget, err := d.budget.Allocate(ctx, call.Messages, tools, BudgetConfig{})
		if err != nil {
			return nil, fmt.Errorf("driver: budget: %w", err)
		}
		if budget.Available <= 0 {
			return nil, errors.New("driver: token budget exhausted")
		}
		if tuningParams.MaxTokens > budget.Available {
			tuningParams.MaxTokens = budget.Available
		}
	}

	// 3. Execute with retries
	var lastErr error
	var result *Result
	retryCount := 0

	for attempt := 1; attempt <= d.cfg.MaxRetries+1; attempt++ {
		if attempt > 1 {
			delay, ok := d.retry.ShouldRetry(attempt-1, lastErr)
			if !ok {
				break
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("driver: retry cancelled: %w", ctx.Err())
			}
		}

		result, lastErr = llmFn(ctx, call.SystemPrompt, call.Messages, tuningParams)
		if lastErr == nil {
			retryCount = attempt - 1
			if eb, ok := d.retry.(interface{ RecordSuccess() }); ok {
				eb.RecordSuccess()
			}
			break
		}
		if eb, ok := d.retry.(interface{ RecordFailure() }); ok {
			eb.RecordFailure()
		}
	}

	elapsed := time.Since(start)

	// 4. Record metrics
	if d.cfg.EnableMetrics {
		cm := CallMetrics{
			Duration:   elapsed,
			Success:    lastErr == nil,
			RetryCount: retryCount,
		}
		if result != nil {
			cm.Tokens = result.TokenUsage
		}
		if lastErr != nil {
			cm.Error = lastErr.Error()
		}
		_ = d.metrics.Record(ctx, cm)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("driver: after %d attempts: %w", retryCount+1, lastErr)
	}

	return result, nil
}

func (d *DefaultDriver) Metrics(ctx context.Context) (*MetricsSnapshot, error) {
	return d.metrics.Snapshot(ctx)
}

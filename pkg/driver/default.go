package driver

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"text/template"
	"time"
)

// DefaultParameterTuner is a pass-through tuner that returns the base
// parameters unchanged. It ignores Call.Metadata entirely.
//
// Consumers who want business-specific tuning (emotion, personality, etc.)
// should implement ParameterTuner and supply it via the DefaultDriver's
// constructor or SetTuner method.
type DefaultParameterTuner struct {
	cfg TuningConfig
}

// TuningConfig controls default parameter values for the passthrough tuner.
type TuningConfig struct {
	DefaultTemperature      float64
	DefaultTopP             float64
	DefaultMaxTokens        int
	DefaultPresencePenalty  float64
	DefaultFrequencyPenalty float64
}

var _ ParameterTuner = (*DefaultParameterTuner)(nil)

func NewDefaultParameterTuner(cfg TuningConfig) *DefaultParameterTuner {
	if cfg.DefaultTemperature == 0 {
		cfg.DefaultTemperature = 0.7
	}
	if cfg.DefaultTopP == 0 {
		cfg.DefaultTopP = 0.9
	}
	if cfg.DefaultMaxTokens == 0 {
		cfg.DefaultMaxTokens = 2048
	}
	return &DefaultParameterTuner{cfg: cfg}
}

func (d *DefaultParameterTuner) Tune(_ context.Context, base TuningParams, _ map[string]any) (TuningParams, error) {
	if base.Temperature == 0 {
		base.Temperature = d.cfg.DefaultTemperature
	}
	if base.TopP == 0 {
		base.TopP = d.cfg.DefaultTopP
	}
	if base.MaxTokens == 0 {
		base.MaxTokens = d.cfg.DefaultMaxTokens
	}
	base.PresencePenalty = d.cfg.DefaultPresencePenalty
	base.FrequencyPenalty = d.cfg.DefaultFrequencyPenalty
	return base, nil
}

// DefaultTokenBudgetManager implements TokenBudgetManager with fixed
// percentages: ~70% context / ~30% response, with a reserve for tool
// definitions.
type DefaultTokenBudgetManager struct {
	cfg BudgetConfig
}

var _ TokenBudgetManager = (*DefaultTokenBudgetManager)(nil)

func NewDefaultTokenBudgetManager(cfg BudgetConfig) *DefaultTokenBudgetManager {
	if cfg.MaxContextTokens == 0 {
		cfg.MaxContextTokens = 128_000
	}
	if cfg.MaxResponseTokens == 0 {
		cfg.MaxResponseTokens = 4096
	}
	if cfg.ReserveForTools == 0 {
		cfg.ReserveForTools = 2000
	}
	return &DefaultTokenBudgetManager{cfg: cfg}
}

func (d *DefaultTokenBudgetManager) Allocate(_ context.Context, msgs []Message, tools []ToolDef, _ BudgetConfig) (*Budget, error) {
	toolBudget := 0
	if len(tools) > 0 {
		toolBudget = d.cfg.ReserveForTools
	}

	contextBudget := d.cfg.MaxContextTokens - d.cfg.MaxResponseTokens - toolBudget
	if contextBudget < 0 {
		contextBudget = 0
	}

	used := 0
	for _, m := range msgs {
		used += estimateTokens(m.Content)
	}

	return &Budget{
		TotalBudget: d.cfg.MaxContextTokens,
		Used:        used,
		Available:   contextBudget - used,
	}, nil
}

func estimateTokens(s string) int {
	var cjk, latin int
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		} else {
			latin++
		}
	}
	return cjk*2 + latin/4 + 4
}

// ExponentialBackoff implements RetryStrategy with exponential backoff
// and a circuit breaker.
type ExponentialBackoff struct {
	mu            sync.Mutex
	maxRetries    int
	baseDelay     time.Duration
	maxDelay      time.Duration
	failCount     int
	failThreshold int
	cooldown      time.Duration
	lastFailAt    time.Time
	state         BreakerState
}

var _ RetryStrategy = (*ExponentialBackoff)(nil)

func NewExponentialBackoff(maxRetries int, baseDelay time.Duration, failThreshold int, cooldown time.Duration) *ExponentialBackoff {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	if failThreshold <= 0 {
		failThreshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &ExponentialBackoff{
		maxRetries:    maxRetries,
		baseDelay:     baseDelay,
		maxDelay:      30 * time.Second,
		failThreshold: failThreshold,
		cooldown:      cooldown,
		state:         BreakerClosed,
	}
}

func (e *ExponentialBackoff) ShouldRetry(attempt int, err error) (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if attempt > e.maxRetries {
		return 0, false
	}

	now := time.Now()
	switch e.state {
	case BreakerOpen:
		if now.After(e.lastFailAt.Add(e.cooldown)) {
			e.state = BreakerHalfOpen
		} else {
			return 0, false
		}
	case BreakerHalfOpen:
	}

	delay := e.baseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
	if delay > e.maxDelay {
		delay = e.maxDelay
	}
	// Add ±25% jitter to prevent thundering herd when multiple callers
	// fail simultaneously and retry in lockstep.
	//nolint:gosec // weak random is acceptable for jitter; no cryptographic need
	jitter := time.Duration(float64(delay) * (rand.Float64() - 0.5) * 0.5)
	delay += jitter
	return delay, true
}

func (e *ExponentialBackoff) CircuitBreaker() BreakerState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *ExponentialBackoff) RecordSuccess() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failCount = 0
	e.state = BreakerClosed
}

func (e *ExponentialBackoff) RecordFailure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failCount++
	e.lastFailAt = time.Now()
	if e.failCount >= e.failThreshold {
		e.state = BreakerOpen
	}
}

// maxP95Samples caps the ring buffer size for P99 latency tracking to
// prevent unbounded memory growth under sustained load.
const maxP95Samples = 1000

// DefaultMetricsCollector is an in-memory metrics aggregator.
type DefaultMetricsCollector struct {
	mu           sync.Mutex
	calls        int64
	errors       int64
	tokens       int64
	totalLatency time.Duration
	p95Values    []time.Duration
	p95Next      int // ring buffer write index; -1 means append mode (< maxP95Samples)
}

var _ MetricsCollector = (*DefaultMetricsCollector)(nil)

func NewDefaultMetricsCollector() *DefaultMetricsCollector {
	return &DefaultMetricsCollector{p95Next: -1}
}

func (d *DefaultMetricsCollector) Record(_ context.Context, m CallMetrics) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.calls++
	d.tokens += int64(m.Tokens.Total)
	d.totalLatency += m.Duration
	if d.p95Next < 0 {
		// Append mode (still below cap).
		d.p95Values = append(d.p95Values, m.Duration)
		if len(d.p95Values) >= maxP95Samples {
			d.p95Next = 0
		}
	} else {
		d.p95Values[d.p95Next] = m.Duration
		d.p95Next = (d.p95Next + 1) % maxP95Samples
	}
	if !m.Success {
		d.errors++
	}
	return nil
}

func (d *DefaultMetricsCollector) Snapshot(_ context.Context) (*MetricsSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	s := &MetricsSnapshot{
		TotalCalls:  d.calls,
		TotalTokens: d.tokens,
		TotalErrors: d.errors,
		ByModel:     make(map[string]ModelMetrics),
		ByProvider:  make(map[string]ProviderMetrics),
	}
	if d.calls > 0 {
		s.AvgLatency = time.Duration(int64(d.totalLatency) / d.calls)
	}
	if d.errors > 0 {
		s.ErrorRate = float64(d.errors) / float64(d.calls)
	}
	if len(d.p95Values) > 0 {
		sorted := make([]time.Duration, len(d.p95Values))
		copy(sorted, d.p95Values)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		idx := int(float64(len(sorted)) * 0.95)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		s.P95Latency = sorted[idx]
	}
	return s, nil
}

func (d *DefaultMetricsCollector) Reset(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.calls = 0
	d.errors = 0
	d.tokens = 0
	d.totalLatency = 0
	d.p95Values = nil
	d.p95Next = -1
	return nil
}

// DefaultPromptEngine is an in-memory template engine backed by text/template.
type DefaultPromptEngine struct {
	mu        sync.Mutex
	templates map[string]*template.Template
}

var _ PromptEngine = (*DefaultPromptEngine)(nil)

func NewDefaultPromptEngine() *DefaultPromptEngine {
	return &DefaultPromptEngine{templates: make(map[string]*template.Template)}
}

func (d *DefaultPromptEngine) Render(name string, data any) (string, error) {
	d.mu.Lock()
	tmpl, ok := d.templates[name]
	d.mu.Unlock()

	if !ok {
		return "", nil
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt render %q: %w", name, err)
	}
	return buf.String(), nil
}

func (d *DefaultPromptEngine) Register(name, tmplStr string) error {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("prompt register %q: %w", name, err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.templates[name] = tmpl
	return nil
}

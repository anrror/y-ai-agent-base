package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// ErrNoProviders is returned when no providers are available for scheduling.
var ErrNoProviders = errors.New("scheduler: no providers")

// LatencyScheduler selects the provider with the lowest P50 latency.
type LatencyScheduler struct{}

var _ Scheduler = (*LatencyScheduler)(nil)

func (l *LatencyScheduler) Strategy() Strategy { return StrategyLatency }

func (l *LatencyScheduler) Schedule(_ context.Context, req *Request, providers []ProviderProfile) (*Decision, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("latency scheduler: %w", ErrNoProviders)
	}
	filtered := filterCandidates(providers, req)
	if len(filtered) == 0 {
		filtered = providers
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Latency < filtered[j].Latency
	})
	return &Decision{
		Selected: &filtered[0],
		Score:    scoreFromLatency(filtered[0].Latency),
		Fallback: len(filtered) < len(providers),
	}, nil
}

func scoreFromLatency(d time.Duration) float64 {
	return 1.0 - math.Min(float64(d.Milliseconds())/10000, 1.0)
}

// CostScheduler selects the cheapest provider.
type CostScheduler struct{}

var _ Scheduler = (*CostScheduler)(nil)

func (c *CostScheduler) Strategy() Strategy { return StrategyCost }

func (c *CostScheduler) Schedule(_ context.Context, req *Request, providers []ProviderProfile) (*Decision, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("cost scheduler: %w", ErrNoProviders)
	}
	filtered := filterCandidates(providers, req)
	if len(filtered) == 0 {
		filtered = providers
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Cost < filtered[j].Cost
	})
	return &Decision{
		Selected: &filtered[0],
		Score:    1.0 - math.Min(filtered[0].Cost/0.1, 1.0),
		Fallback: len(filtered) < len(providers),
	}, nil
}

// BalancedScheduler uses a weighted score: 40 % latency, 40 % cost, 20 % priority.
type BalancedScheduler struct{}

var _ Scheduler = (*BalancedScheduler)(nil)

func (b *BalancedScheduler) Strategy() Strategy { return StrategyBalanced }

func (b *BalancedScheduler) Schedule(_ context.Context, req *Request, providers []ProviderProfile) (*Decision, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("balanced scheduler: %w", ErrNoProviders)
	}
	filtered := filterCandidates(providers, req)
	if len(filtered) == 0 {
		filtered = providers
	}

	// Compute normalised scores.
	maxLat := maxLatency(filtered)
	maxCost := maxCost(filtered)
	maxPrio := maxPriority(filtered)

	type scored struct {
		profile ProviderProfile
		score   float64
	}
	candidates := make([]scored, len(filtered))
	for i, p := range filtered {
		latScore := 1.0
		if maxLat > 0 {
			latScore = 1.0 - float64(p.Latency.Milliseconds())/float64(maxLat.Milliseconds())
		}
		costScore := 1.0
		if maxCost > 0 {
			costScore = 1.0 - p.Cost/maxCost
		}
		prioScore := 0.0
		if maxPrio > 0 {
			prioScore = float64(p.Priority) / float64(maxPrio)
		}
		candidates[i] = scored{
			profile: p,
			score:   0.4*latScore + 0.4*costScore + 0.2*prioScore,
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	return &Decision{
		Selected: &candidates[0].profile,
		Score:    candidates[0].score,
		Fallback: len(filtered) < len(providers),
	}, nil
}

// PriorityScheduler selects by highest priority, then lowest latency as tiebreaker.
type PriorityScheduler struct{}

var _ Scheduler = (*PriorityScheduler)(nil)

func (p *PriorityScheduler) Strategy() Strategy { return StrategyPriority }

func (p *PriorityScheduler) Schedule(_ context.Context, req *Request, providers []ProviderProfile) (*Decision, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("priority scheduler: %w", ErrNoProviders)
	}
	filtered := filterCandidates(providers, req)
	if len(filtered) == 0 {
		filtered = providers
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Priority != filtered[j].Priority {
			return filtered[i].Priority > filtered[j].Priority
		}
		return filtered[i].Latency < filtered[j].Latency
	})
	return &Decision{
		Selected: &filtered[0],
		Score:    float64(filtered[0].Priority) / 100.0,
		Fallback: len(filtered) < len(providers),
	}, nil
}

// --- helpers ---

func filterCandidates(pp []ProviderProfile, req *Request) []ProviderProfile {
	if req.MaxLatency == 0 && req.MaxCost == 0 && req.PreferredModel == "" {
		return pp
	}
	out := make([]ProviderProfile, 0, len(pp))
	for _, p := range pp {
		if req.MaxLatency > 0 && p.Latency > req.MaxLatency {
			continue
		}
		if req.MaxCost > 0 && p.Cost > req.MaxCost {
			continue
		}
		if req.PreferredModel != "" && p.Model != req.PreferredModel {
			continue
		}
		out = append(out, p)
	}
	return out
}

func maxLatency(pp []ProviderProfile) time.Duration {
	var m time.Duration
	for _, p := range pp {
		if p.Latency > m {
			m = p.Latency
		}
	}
	return m
}

func maxCost(pp []ProviderProfile) float64 {
	var m float64
	for _, p := range pp {
		if p.Cost > m {
			m = p.Cost
		}
	}
	return m
}

func maxPriority(pp []ProviderProfile) int {
	var m int
	for _, p := range pp {
		if p.Priority > m {
			m = p.Priority
		}
	}
	return m
}

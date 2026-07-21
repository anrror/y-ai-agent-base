package inference

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNoHealthyEndpoint is returned when no healthy provider endpoint matches
// the given request requirements.
var ErrNoHealthyEndpoint = errors.New("inference: no healthy endpoint")

// Registry is a thread-safe store for provider endpoint metadata.
type Registry struct {
	mu        sync.RWMutex
	providers []ProviderInfo
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Add(info ProviderInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Replace if exists, otherwise append.
	for i, p := range r.providers {
		if p.Name == info.Name && p.Model == info.Model {
			r.providers[i] = info
			return
		}
	}
	r.providers = append(r.providers, info)
}

func (r *Registry) Remove(name, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	remaining := r.providers[:0]
	for _, p := range r.providers {
		if p.Name == name && p.Model == model {
			continue
		}
		remaining = append(remaining, p)
	}
	r.providers = remaining
}

func (r *Registry) List() []ProviderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderInfo, len(r.providers))
	copy(out, r.providers)
	return out
}

// DefaultRouter implements Router with capability matching, priority
// ordering, and health filtering.
type DefaultRouter struct {
	reg *Registry
}

var _ Router = (*DefaultRouter)(nil)

func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{reg: NewRegistry()}
}

func (d *DefaultRouter) Resolve(_ context.Context, req *Request) (*Endpoint, error) {
	candidates := d.reg.List()

	// Filter by capability and health.
	filtered := make([]ProviderInfo, 0, len(candidates))
	for _, p := range candidates {
		if p.Health != HealthHealthy {
			continue
		}
		if !hasCapability(p.Capabilities, req.Capability) {
			continue
		}
		if req.Model != "" && p.Model != req.Model {
			continue
		}
		filtered = append(filtered, p)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("%w for model %q capability %q", ErrNoHealthyEndpoint, req.Model, req.Capability)
	}

	// Sort by priority (highest first).
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Priority > filtered[j].Priority
	})

	best := filtered[0]
	return &Endpoint{
		ProviderName: best.Name,
		Model:        best.Model,
		Capability:   req.Capability,
		BaseURL:      best.BaseURL,
		Health:       best.Health,
	}, nil
}

func (d *DefaultRouter) Register(_ context.Context, info ProviderInfo) error {
	d.reg.Add(info)
	return nil
}

func (d *DefaultRouter) Unregister(_ context.Context, name, model string) error {
	d.reg.Remove(name, model)
	return nil
}

func (d *DefaultRouter) Health(_ context.Context) map[string]HealthStatus {
	out := make(map[string]HealthStatus)
	for _, p := range d.reg.List() {
		out[p.Name+"|"+p.Model] = p.Health
	}
	return out
}

func hasCapability(caps []Capability, target Capability) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

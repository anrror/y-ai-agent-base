// Package handler provides HTTP request handlers for the server API.
package handler

import (
	"sync"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/config"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/store"
	"github.com/anrror/y-ai-agent-base/pkg/team"
)

// Handler holds all dependencies for HTTP request handlers.
type Handler struct {
	mu              sync.RWMutex
	AgentRegistry   *agent.Registry
	AgentStore      store.AgentStore
	Cfg             *config.Config
	Provider        provider.LLMProvider      // chat provider for backward compat
	Providers       *provider.ProviderSet     // full provider set (Chat, Embedding, Guard)
	Pipeline        pipeline.Pipeline
	PipelineMetrics *pipeline.Metrics

	// TeamRegistry holds all registered multi-agent teams.
	TeamRegistry *team.Registry
}

// New creates a Handler with required dependencies.
func New(
	reg *agent.Registry,
	store store.AgentStore,
	cfg *config.Config,
	ps *provider.ProviderSet,
	pipe pipeline.Pipeline,
	metrics *pipeline.Metrics,
) *Handler {
	return &Handler{
		AgentRegistry:   reg,
		AgentStore:      store,
		Cfg:             cfg,
		Provider:        ps.Chat, // chat provider for backward compat
		Providers:       ps,
		Pipeline:        pipe,
		PipelineMetrics: metrics,
		TeamRegistry:    team.NewRegistry(),
	}
}

// ReloadSharedState atomically swaps the shared config, provider, and pipeline
// references. Called by config.Watch callback. Safe for concurrent use with
// HTTP handlers that read these fields.
// When prov is nil the existing provider is kept (config-only reload);
// when pipe is nil the existing pipeline is kept.
func (h *Handler) ReloadSharedState(cfg *config.Config, prov provider.LLMProvider, pipe pipeline.Pipeline) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Cfg = cfg
	if prov != nil {
		h.Provider = prov
	}
	if pipe != nil {
		h.Pipeline = pipe
	}
}

// ReadProvider returns the current provider under read lock.
func (h *Handler) ReadProvider() provider.LLMProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Provider
}

// ReadPipeline returns the current pipeline under read lock.
func (h *Handler) ReadPipeline() pipeline.Pipeline {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Pipeline
}

// Package server builds and runs the HTTP server with module support.
//
// Usage:
//
//	srv, err := server.New(cfg,
//	    server.WithModule(crmModule),
//	    server.WithModule(billingModule),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	srv.Run()
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/internal/handler"
	srvcfg "github.com/anrror/y-ai-agent-base/internal/config"
	"github.com/anrror/y-ai-agent-base/internal/middleware"
	"github.com/anrror/y-ai-agent-base/pkg/agent"
	appcfg "github.com/anrror/y-ai-agent-base/pkg/config"
	"github.com/anrror/y-ai-agent-base/pkg/module"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/provider/openai"
	"github.com/anrror/y-ai-agent-base/pkg/store"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

const defaultChatTimeout = 30 * time.Second

// Server is the HTTP server with module lifecycle management.
type Server struct {
	cfg     *appcfg.Config
	sc      srvcfg.ServerConfig
	modules []module.ModuleInfo

	// Core framework components — provMu guards prov, ps, and pipe for hot-reload.
	prov    provider.LLMProvider
	ps      *provider.ProviderSet
	pipe    pipeline.Pipeline
	provMu  sync.RWMutex
	metrics *pipeline.Metrics
	as      *store.MemoryStore
	reg     *agent.Registry

	// HTTP layer.
	gin     *gin.Engine
	h       *handler.Handler
	mw      *middleware.Middleware
	srv     *http.Server

	// Module registrations collected during Init.
	mu            sync.Mutex
	healthChecks  []module.HealthCheckRegistration
	routes        []module.RouteRegistration
	middlewares   []gin.HandlerFunc

	// Seed functions (run after buildCore, before buildHandler).
	seedFns []func(*agent.Registry, provider.LLMProvider, pipeline.Pipeline) error

	// Team builders (run after buildHandler, before setupRoutes).
	teamBuilders []func(*handler.Handler, *agent.Registry, provider.LLMProvider, pipeline.Pipeline) error

	// Lifecycle.
	watchCtx    context.Context
	watchCancel context.CancelFunc
}

// New creates a Server with the given config and options.
func New(cfg *appcfg.Config, opts ...Option) (*Server, error) {
	sc := srvcfg.FromAppConfig(cfg)

	s := &Server{
		cfg: cfg,
		sc:  sc,
	}

	// Apply options (registers modules).
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	// Build core framework components.
	s.buildCore()

	// Run seed functions (e.g. demo agent seeding).
	for _, fn := range s.seedFns {
		if err := fn(s.reg, s.prov, s.pipe); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}

	s.buildHandler()
	s.buildGin()

	// Build teams (TeamRegistry is now available via handler).
	for _, fn := range s.teamBuilders {
		if err := fn(s.h, s.reg, s.prov, s.pipe); err != nil {
			return nil, fmt.Errorf("team build: %w", err)
		}
	}

	// Init modules.
	if err := s.initModules(); err != nil {
		return nil, fmt.Errorf("module init: %w", err)
	}

	// Setup Gin routes (framework + module).
	s.setupRoutes()

	return s, nil
}

// ─── Option ──────────────────────────────────────────────────────────────────

// Option configures the Server.
type Option func(*Server) error

// WithModule adds a business module to the server.
func WithModule(m module.Module) Option {
	return func(s *Server) error {
		s.modules = append(s.modules, module.ModuleInfo{Module: m})
		return nil
	}
}

// WithSeed registers a function that seeds initial agents into the registry.
// The function runs after core components are built but before HTTP handlers
// are initialized. Multiple WithSeed calls are executed in order.
func WithSeed(fn func(reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error) Option {
	return func(s *Server) error {
		s.seedFns = append(s.seedFns, fn)
		return nil
	}
}

// WithTeam registers a function that builds multi-agent teams and registers
// them with the handler's TeamRegistry. These functions run after the handler
// is built (so TeamRegistry is available) but before routes are set up.
//
// The function receives the handler (for TeamRegistry access), the agent
// registry, provider, and pipeline.
func WithTeam(fn func(h *handler.Handler, reg *agent.Registry, prov provider.LLMProvider, pipe pipeline.Pipeline) error) Option {
	return func(s *Server) error {
		s.teamBuilders = append(s.teamBuilders, fn)
		return nil
	}
}

// ─── Core Builders (private) ─────────────────────────────────────────────────

func (s *Server) buildCore() {
	ps := &provider.ProviderSet{}

	if s.cfg.Providers.Chat != nil {
		ps.Chat = openai.NewOpenAIProvider(&provider.ProviderConfig{
			Type:    s.cfg.Providers.Chat.Type,
			APIKey:  s.cfg.Providers.Chat.APIKey,
			BaseURL: s.cfg.Providers.Chat.BaseURL,
			Model:   s.cfg.Providers.Chat.Model,
		})
	}
	if s.cfg.Providers.Embedding != nil {
		ps.Embedding = openai.NewOpenAIProvider(&provider.ProviderConfig{
			Type:    s.cfg.Providers.Embedding.Type,
			APIKey:  s.cfg.Providers.Embedding.APIKey,
			BaseURL: s.cfg.Providers.Embedding.BaseURL,
			Model:   s.cfg.Providers.Embedding.Model,
		})
	}
	if s.cfg.Providers.Guard != nil {
		ps.Guard = openai.NewOpenAIProvider(&provider.ProviderConfig{
			Type:    s.cfg.Providers.Guard.Type,
			APIKey:  s.cfg.Providers.Guard.APIKey,
			BaseURL: s.cfg.Providers.Guard.BaseURL,
			Model:   s.cfg.Providers.Guard.Model,
		})
	}

	s.prov = ps.Chat // fallback to chat provider for LLMProvider interface where needed

	s.metrics = pipeline.NewMetrics()
	s.pipe = pipeline.New(s.prov,
		pipeline.MetricsMiddleware(s.metrics),
		pipeline.Timeout(defaultChatTimeout),
	)
	s.ps = ps // store ProviderSet
	s.as = store.NewMemoryStore()
	s.reg = agent.NewRegistry()
}

func (s *Server) buildHandler() {
	telemetry := middleware.NewTelemetryHook(slog.Default())
	s.mw = middleware.New(s.sc.JWTSecret, telemetry)
	s.mw.RateLimitCfg.Enabled = s.sc.RateLimit.Enabled
	s.mw.RateLimitCfg.RequestsPerMin = s.sc.RateLimit.RequestsPerMin
	s.mw.RateLimitCfg.Burst = s.sc.RateLimit.Burst

	s.h = handler.New(s.reg, s.as, s.cfg, s.ps, s.pipe, s.metrics)
}

func (s *Server) buildGin() {
	s.gin = gin.New()
	s.gin.Use(middleware.Recovery())
	s.gin.Use(s.mw.CORSHandler())
	s.gin.Use(middleware.RequestID())
	s.gin.Use(s.mw.Logging())
	s.gin.Use(s.mw.RateLimitHandler())
}

// ─── Module Initialization ───────────────────────────────────────────────────

func (s *Server) initModules() error {
	for i := range s.modules {
		m := &s.modules[i]
		if err := s.initModule(m); err != nil {
			return fmt.Errorf("module %q: %w", m.Module.ID(), err)
		}
	}
	return nil
}

func (s *Server) initModule(mi *module.ModuleInfo) error {
	mod := mi.Module
	id := mod.ID()

	// Parse config.
	if cfgType := mod.Config(); cfgType != nil {
		if err := s.cfg.ModuleConfig(id, cfgType); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		mi.Config = cfgType
	}

	// Create logger.
	mi.Logger = slog.With("module", id)

	// Build InitContext.
	ctx := &module.Context{
		ModuleConfig: mi.Config,
		Logger:       mi.Logger,
		Router: func() gin.IRouter {
			return s.gin.Group("/api/v1/" + id)
		},
		RegisterRoute:       s.registerRoute,
		RegisterHealthCheck: s.registerHealthCheck,
		RegisterMiddleware:  s.registerMiddleware,
		AgentRegistry:       s.reg,
		AgentStore:          s.as,
		Provider:            s.prov,
		Pipeline:            s.pipe,
	}

	return mod.Init(ctx)
}

func (s *Server) registerRoute(method, path string, handler gin.HandlerFunc, mws ...gin.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = append(s.routes, module.RouteRegistration{
		Method: method, Path: path, Handler: handler, Middleware: mws,
	})
}

func (s *Server) registerHealthCheck(name string, fn module.HealthCheckFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthChecks = append(s.healthChecks, module.HealthCheckRegistration{Name: name, Fn: fn})
}

func (s *Server) registerMiddleware(mw gin.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.middlewares = append(s.middlewares, mw)
}

// ─── Route Setup ─────────────────────────────────────────────────────────────

func (s *Server) setupRoutes() {
	// Apply module middlewares.
	for _, mw := range s.middlewares {
		s.gin.Use(mw)
	}

	// Framework routes.
	s.gin.GET("/health", s.healthHandler)
	s.gin.GET("/metrics", s.h.Metrics)

	v1 := s.gin.Group("/api/v1")
	v1.Use(s.mw.JWTAuth())
	{
		v1.POST("/chat/completions", middleware.RequestTimeout(defaultChatTimeout), s.h.ChatCompletions)
		v1.POST("/agents", s.h.RegisterAgent)
		v1.GET("/agents", s.h.ListAgents)
		v1.GET("/agents/:id", s.h.GetAgent)
		v1.DELETE("/agents/:id", s.h.DeleteAgent)

		// Multi-agent team routes.
		v1.GET("/teams", s.h.ListTeams)
		v1.GET("/teams/:id", s.h.GetTeam)
	}

	// Module routes.
	for _, r := range s.routes {
		s.gin.Handle(r.Method, r.Path, append(r.Middleware, r.Handler)...)
	}
}

// ─── Health Handler (with module checks) ─────────────────────────────────────

func (s *Server) healthHandler(c *gin.Context) {
	var status, dbStatus, providerStatus = "ok", "ok", "ok"

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.as.LoadAll(ctx); err != nil {
		dbStatus = string(module.HealthDegraded)
	}

	pingCtx, pingCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer pingCancel()
	s.provMu.RLock()
	prov := s.prov
	s.provMu.RUnlock()
	if err := prov.Ping(pingCtx); err != nil {
		providerStatus = string(module.HealthDegraded)
	}

	if dbStatus != "ok" || providerStatus != "ok" {
		status = string(module.HealthDegraded)
	}

	// Module health checks.
	checks := gin.H{
		"database": dbStatus,
		"provider": providerStatus,
	}
	allOK := dbStatus == "ok" && providerStatus == "ok"

	for _, hc := range s.healthChecks {
		hcCtx, hcCancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		report := hc.Fn(hcCtx)
		hcCancel()
		checks[hc.Name] = gin.H{
			"status":    report.Status,
			"latency":   report.Latency,
			"error":     report.Error,
			"timestamp": report.Timestamp,
		}
		if report.Status != module.HealthOK {
			allOK = false
		}
	}

	if !allOK {
		status = string(module.HealthDegraded)
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"checks":    checks,
	})
}

// ─── Run / Shutdown ──────────────────────────────────────────────────────────

// Run starts the HTTP server and blocks until SIGINT/SIGTERM.
func (s *Server) Run() error {
	setupLogging(s.sc)

	// Start modules (propagate watchCtx so modules can observe shutdown).
	for _, mi := range s.modules {
		if err := mi.Module.Start(s.watchCtx); err != nil {
			return fmt.Errorf("module %q start: %w", mi.Module.ID(), err)
		}
	}

	// Config file watching.
	s.watchCtx, s.watchCancel = context.WithCancel(context.Background())
	go s.watchConfig()

	// HTTP server.
	s.srv = &http.Server{
		Addr:         s.sc.Addr(),
		Handler:      s.gin,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("listening", "addr", s.sc.Addr())
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen", "error", err)
		}
	}()

	sig := <-quit
	slog.Info("shutting down", "signal", sig.String())

	// Graceful shutdown.
	s.watchCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Stop modules (in reverse order).
	for i := len(s.modules) - 1; i >= 0; i-- {
		if err := s.modules[i].Module.Stop(shutdownCtx); err != nil {
			slog.Error("module stop", "module", s.modules[i].Module.ID(), "error", err)
		}
	}

	_ = s.srv.Shutdown(shutdownCtx)

	// Flush any in-flight pipeline post-hooks before closing provider.
	if p, ok := s.pipe.(interface{ ShutdownPostHooks() }); ok {
		p.ShutdownPostHooks()
	}

	s.provMu.RLock()
	prov := s.prov
	s.provMu.RUnlock()
	if closer, ok := prov.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	_ = s.as.Close()
	for _, ag := range s.reg.List() {
		_ = ag.Close()
	}

	slog.Info("server stopped")
	return nil
}

// ─── Config Watch ────────────────────────────────────────────────────────────

func (s *Server) watchConfig() {
	slog.Info("config: watching for changes", "file", "config/config.yaml")

	if err := appcfg.Watch(s.watchCtx, func(newCfg *appcfg.Config) {
		slog.Info("config: reload triggered")

		chatChanged := newCfg.Providers.Chat.APIKey != s.cfg.Providers.Chat.APIKey ||
			newCfg.Providers.Chat.BaseURL != s.cfg.Providers.Chat.BaseURL ||
			newCfg.Providers.Chat.Model != s.cfg.Providers.Chat.Model

		var newProv provider.LLMProvider
		var newPipe pipeline.Pipeline
		if chatChanged {
			slog.Info("config: Chat provider config changed, re-creating provider",
				"model", newCfg.Providers.Chat.Model)
			newProv = openai.NewOpenAIProvider(&provider.ProviderConfig{
				Type:    newCfg.Providers.Chat.Type,
				APIKey:  newCfg.Providers.Chat.APIKey,
				BaseURL: newCfg.Providers.Chat.BaseURL,
				Model:   newCfg.Providers.Chat.Model,
			})
			newPipe = pipeline.New(newProv,
				pipeline.MetricsMiddleware(s.metrics),
				pipeline.Timeout(defaultChatTimeout),
			)
		}

		// Reload agent configs.
		for _, ag := range s.reg.List() {
			existingCfg := ag.GetConfig()
			agentCfg := agent.Config{
				AgentID:     existingCfg.AgentID,
				Identity:    existingCfg.Identity,
				Personality: existingCfg.Personality,
				LLMConfig: types.ModelConfig{
					Model:       newCfg.Providers.Chat.Model,
					Temperature: existingCfg.LLMConfig.Temperature,
					MaxTokens:   existingCfg.LLMConfig.MaxTokens,
				},
				PromptTmpl: existingCfg.PromptTmpl,
				Status:     existingCfg.Status,
			}

			if err := ag.ReloadConfig(agentCfg); err != nil {
				slog.Error("config: agent reload failed", "agent", ag.ID(), "error", err)
				continue
			}

			if chatChanged {
				ag.ReloadProvider(newProv, newPipe)
			}
		}

		// Atomically update handler's shared state (deep copy to avoid
		// aliasing the Modules map between old and new config).
		cfgCopy := new(appcfg.Config)
		*cfgCopy = *newCfg
		if len(newCfg.Modules) > 0 {
			cfgCopy.Modules = make(map[string]any, len(newCfg.Modules))
			for mk, mv := range newCfg.Modules {
				cfgCopy.Modules[mk] = mv
			}
		}
		s.h.ReloadSharedState(cfgCopy, newProv, newPipe)
		s.cfg = cfgCopy

		if chatChanged {
			// Atomically swap provider and pipeline under provMu.
			s.provMu.Lock()
			oldProv := s.prov
			s.prov = newProv
			s.pipe = newPipe
			s.provMu.Unlock()

			// Close old provider outside the lock.
			if closer, ok := oldProv.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}

		slog.Info("config: reload complete")
	}); err != nil {
		slog.Error("config: file watch start failed, config reload disabled", "file", "config/config.yaml", "error", err)
	}
}

// ─── Logging ─────────────────────────────────────────────────────────────────

// ─── Accessors ───────────────────────────────────────────────────────────────

// Registry returns the agent registry.
func (s *Server) Registry() *agent.Registry { return s.reg }

// Provider returns the LLM provider (thread-safe).
func (s *Server) Provider() provider.LLMProvider {
	s.provMu.RLock()
	defer s.provMu.RUnlock()
	return s.prov
}

// Pipeline returns the middleware pipeline (thread-safe).
func (s *Server) Pipeline() pipeline.Pipeline {
	s.provMu.RLock()
	defer s.provMu.RUnlock()
	return s.pipe
}

// Handler returns the HTTP handler.
func (s *Server) Handler() *handler.Handler { return s.h }

// Gin returns the Gin engine.
func (s *Server) Gin() *gin.Engine { return s.gin }

// ─── Logging ─────────────────────────────────────────────────────────────────

func setupLogging(sc srvcfg.ServerConfig) {
	level := new(slog.LevelVar)
	switch sc.LogLevel {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	opts := &slog.HandlerOptions{Level: level}
	var hdlr slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if sc.LogFormat == "text" {
		hdlr = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(hdlr))
}

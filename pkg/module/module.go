// Package module defines the plugin system for pluggable business modules.
//
// A module is a self-contained business component that can register its own
// configuration, HTTP routes, health checks, and middleware without modifying
// framework internals. Modules implement the Module interface and are passed
// to the server builder via WithModule().
//
// Architecture
//
//	main.go                     — load config, create server, run
//	  └─ server.New(cfg, WithModule(crm), WithModule(billing))
//	       ├─ Parse framework config (pkg/config)
//	       ├─ Create core components (provider, pipeline, registry)
//	       ├─ Init modules — each module registers routes, health checks
//	       ├─ Mount framework routes + module routes
//	       └─ Start modules → Serve HTTP
//
// Business modules live in their own packages (e.g. internal/crm/) and import
// pkg/module for the Module interface and Context.
package module

import (
	"context"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/pkg/agent"
	"github.com/anrror/y-ai-agent-base/pkg/pipeline"
	"github.com/anrror/y-ai-agent-base/pkg/provider"
	"github.com/anrror/y-ai-agent-base/pkg/store"
)

// ─── Health Check Types ──────────────────────────────────────────────────────

// HealthStatus represents a component's health state.
type HealthStatus string

const (
	HealthOK       HealthStatus = "ok"
	HealthDegraded HealthStatus = "degraded"
	HealthDown     HealthStatus = "down"
)

// HealthReport is the result of a health check.
type HealthReport struct {
	Status    HealthStatus `json:"status"`
	Latency   string       `json:"latency,omitempty"`
	Error     string       `json:"error,omitempty"`
	Timestamp time.Time    `json:"timestamp"`
}

// NewHealthReport returns an "ok" HealthReport with the current timestamp.
func NewHealthReport() HealthReport {
	return HealthReport{Status: HealthOK, Timestamp: time.Now()}
}

// HealthReportError returns a "down" HealthReport with the given error.
func HealthReportError(err error) HealthReport {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return HealthReport{Status: HealthDown, Error: msg, Timestamp: time.Now()}
}

// HealthCheckFunc performs a synchronous health check.
type HealthCheckFunc func(ctx context.Context) HealthReport

// ─── Module Interface ────────────────────────────────────────────────────────

// Module is the interface for pluggable business modules.
//
// Lifecycle:
//  1. Config()  — called first, returns config struct pointer
//  2. Init()    — called after config is parsed, module registers routes/checks
//  3. Start()   — called after all modules are initialized (background goroutines)
//  4. Stop()    — called during server shutdown
type Module interface {
	// ID returns a unique module identifier.
	// Used as the config key (modules.<id>) and default route prefix (/api/v1/<id>).
	ID() string

	// Config returns a pointer to the module's configuration struct.
	// The framework reads the "modules.<id>" YAML section into this struct
	// before Init is called. Return nil if the module has no configuration.
	Config() any

	// Init initializes the module with framework services.
	// Modules register routes, health checks, and middleware here.
	Init(ctx *Context) error

	// Start starts background goroutines. The context is cancelled when the
	// server shuts down. Modules should return when ctx is done.
	Start(ctx context.Context) error

	// Stop stops the module gracefully. Called during server shutdown.
	Stop(ctx context.Context) error
}

// ─── Module Context ──────────────────────────────────────────────────────────

// Context provides framework services to Module.Init().
type Context struct {
	// ModuleConfig is the module's parsed configuration (from Config()).
	ModuleConfig any

	// Logger is a structured logger with the module ID attached.
	Logger *slog.Logger

	// Router returns a Gin router group mounted at /api/v1/<module-id>.
	// Routes registered here are JWT-protected by default.
	// Call Router() inside Init() — it is safe only during Init.
	Router func() gin.IRouter

	// RegisterRoute adds a route at an arbitrary path on the Gin engine.
	// Use this for routes outside the module's default prefix, or for
	// unauthenticated endpoints.
	RegisterRoute func(method, path string, handler gin.HandlerFunc, mws ...gin.HandlerFunc)

	// RegisterHealthCheck registers a named health check function.
	// The function is called on every /health request.
	RegisterHealthCheck func(name string, fn HealthCheckFunc)

	// RegisterMiddleware registers a global Gin middleware.
	RegisterMiddleware func(mw gin.HandlerFunc)

	// Framework dependencies (optional — nil when unavailable).
	AgentRegistry *agent.Registry
	AgentStore    store.AgentStore
	Provider      provider.LLMProvider
	Pipeline      pipeline.Pipeline
}

// ─── Module Info ─────────────────────────────────────────────────────────────

// ModuleInfo holds runtime state for a registered module.
type ModuleInfo struct {
	Module Module
	Config any
	Logger *slog.Logger
}

// ModuleManager manages module lifecycle and registrations.
type ModuleManager struct {
	modules      []ModuleInfo
	healthChecks []HealthCheckRegistration
	routes       []RouteRegistration
	middlewares  []gin.HandlerFunc
}

// HealthCheckRegistration binds a name to a health check function.
type HealthCheckRegistration struct {
	Name string
	Fn   HealthCheckFunc
}

// RouteRegistration binds a route to a handler.
type RouteRegistration struct {
	Method     string
	Path       string
	Handler    gin.HandlerFunc
	Middleware []gin.HandlerFunc
}

// NewModuleManager creates an empty ModuleManager.
func NewModuleManager() *ModuleManager {
	return &ModuleManager{}
}

// Add registers a module with the manager.
func (mm *ModuleManager) Add(m Module) {
	mm.modules = append(mm.modules, ModuleInfo{Module: m})
}

// Modules returns all registered modules.
func (mm *ModuleManager) Modules() []ModuleInfo { return mm.modules }

// HealthChecks returns all registered health checks.
func (mm *ModuleManager) HealthChecks() []HealthCheckRegistration {
	return mm.healthChecks
}

// Routes returns all registered routes.
func (mm *ModuleManager) Routes() []RouteRegistration { return mm.routes }

// Middlewares returns all registered global middlewares.
func (mm *ModuleManager) Middlewares() []gin.HandlerFunc { return mm.middlewares }



// Package router sets up all HTTP routes for the server.
package router

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/internal/handler"
	"github.com/anrror/y-ai-agent-base/internal/middleware"
	"github.com/anrror/y-ai-agent-base/pkg/module"
)

// defaultRequestTimeout is the fallback per-request timeout applied to chat
// completions when the client does not supply X-Timeout / X-Request-Timeout.
const defaultRequestTimeout = 30 * time.Second

// Setup creates and configures a Gin engine with all routes and middleware.
//
// Deprecated: Use server.New() which handles module routes automatically.
// Kept for backward compatibility.
func Setup(h *handler.Handler, mw *middleware.Middleware) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	// Global middleware.
	r.Use(middleware.Recovery())
	r.Use(mw.CORSHandler())
	r.Use(middleware.RequestID())
	r.Use(mw.Logging())
	r.Use(mw.RateLimitHandler())

	// Public routes.
	r.GET("/health", h.Health)
	r.GET("/metrics", h.Metrics)

	// API v1 — protected routes.
	v1 := r.Group("/api/v1")
	v1.Use(mw.JWTAuth())
	{
		// Chat completions with per-request timeout support.
		v1.POST("/chat/completions", middleware.RequestTimeout(defaultRequestTimeout), h.ChatCompletions)

		// Agent CRUD.
		v1.POST("/agents", h.RegisterAgent)
		v1.GET("/agents", h.ListAgents)
		v1.GET("/agents/:id", h.GetAgent)
		v1.DELETE("/agents/:id", h.DeleteAgent)
	}

	return r
}

// SetupWithModules creates a Gin engine, applies middleware, mounts framework
// routes, and mounts module-provided routes and health checks.
func SetupWithModules(
	h *handler.Handler,
	mw *middleware.Middleware,
	mods *module.ModuleManager,
	healthHandler gin.HandlerFunc,
) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	// Global middleware.
	r.Use(middleware.Recovery())
	r.Use(mw.CORSHandler())
	r.Use(middleware.RequestID())
	r.Use(mw.Logging())
	r.Use(mw.RateLimitHandler())

	// Module global middleware (registered after framework middleware).
	for _, mwFn := range mods.Middlewares() {
		r.Use(mwFn)
	}

	// Public routes.
	if healthHandler != nil {
		r.GET("/health", healthHandler)
	} else {
		r.GET("/health", h.Health)
	}
	r.GET("/metrics", h.Metrics)

	// API v1 — protected routes.
	v1 := r.Group("/api/v1")
	v1.Use(mw.JWTAuth())
	{
		v1.POST("/chat/completions", middleware.RequestTimeout(defaultRequestTimeout), h.ChatCompletions)
		v1.POST("/agents", h.RegisterAgent)
		v1.GET("/agents", h.ListAgents)
		v1.GET("/agents/:id", h.GetAgent)
		v1.DELETE("/agents/:id", h.DeleteAgent)
	}

	// Module routes.
	for _, route := range mods.Routes() {
		r.Handle(route.Method, route.Path, append(route.Middleware, route.Handler)...)
	}

	return r
}

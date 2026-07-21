// Package middleware provides Gin middleware for the HTTP server.
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// TelemetryHook is the interface for pluggable request observability.
type TelemetryHook interface {
	// BeforeRequest is called after the request context is populated
	// but before the handler executes.
	BeforeRequest(c *gin.Context)
	// AfterRequest is called after the handler completes.
	// latency is the wall-clock duration of the handler execution.
	AfterRequest(c *gin.Context, latency time.Duration)
}

// DefaultTelemetryHook implements TelemetryHook with structured slog JSON
// logging. It emits a single log line per request with fields:
// request_id, method, path, status, latency_ms, agent_id, error.
type DefaultTelemetryHook struct {
	Logger *slog.Logger
}

// NewTelemetryHook creates a DefaultTelemetryHook that writes to the given logger.
// If logger is nil, slog.Default() is used.
func NewTelemetryHook(logger *slog.Logger) *DefaultTelemetryHook {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultTelemetryHook{Logger: logger}
}

// BeforeRequest stores the start time in the Gin context.
func (h *DefaultTelemetryHook) BeforeRequest(c *gin.Context) {
	c.Set("telemetry_start", time.Now())
}

// AfterRequest logs the request summary as a single structured JSON line.
func (h *DefaultTelemetryHook) AfterRequest(c *gin.Context, latency time.Duration) {
	rid, _ := c.Get(string(types.CtxRequestID))
	aid, _ := c.Get(string(types.CtxAgentID))

	attrs := []slog.Attr{
		slog.String("request_id", strVal(rid)),
		slog.String("method", c.Request.Method),
		slog.String("path", c.Request.URL.Path),
		slog.Int("status", c.Writer.Status()),
		slog.Int64("latency_ms", latency.Milliseconds()),
		slog.String("agent_id", strVal(aid)),
	}

	msg := "request completed"
	if len(c.Errors) > 0 {
		attrs = append(attrs, slog.String("error", c.Errors.String()))
		msg = "request failed"
	}

	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, msg, attrs...)
}

// strVal returns the string representation of an any value from a Gin context.
func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case types.ContextKey:
		return string(val)
	default:
		return ""
	}
}

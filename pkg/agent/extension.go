package agent

import "github.com/anrror/y-ai-agent-base/pkg/pipeline"

// Extension is the minimal contract for external modules (emotion, reasoning,
// scheduler, cache, compressor, edge, driver, etc.) that want to plug into an
// Agent without the framework knowing their concrete type.
//
// Use WithExtensions() on the Builder to attach extensions at agent creation
// time. The Agent's Close() method iterates all extensions and calls Close()
// on each, in insertion order.
type Extension interface {
	// ID is a unique identifier for this extension, e.g. "emotion".
	// Must be non-empty. Two extensions with the same ID are allowed; the
	// later one replaces the earlier.
	ID() string

	// Close releases resources held by the extension. Called automatically
	// when Agent.Close() runs. Implementations must be idempotent.
	Close() error
}

// MiddlewareProvider is an optional sub-interface of Extension. When an
// Extension also implements MiddlewareProvider, its Middleware is
// automatically injected into the agent's pipeline at build time (before
// Build() returns). The middleware is appended after any middleware already
// configured on the Pipeline.
//
// This lets external packages (emotion detection, context compression,
// caching, etc.) contribute pipeline behaviour without the Agent Builder
// knowing about them individually.
type MiddlewareProvider interface {
	Extension

	// Middleware returns a pipeline middleware that the Extension contributes.
	Middleware() pipeline.Middleware
}

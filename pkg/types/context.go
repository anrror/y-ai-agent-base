package types

// ContextKey is a typed context key to prevent collisions between packages.
type ContextKey string

// Typed context keys for values carried in context.Context.
const (
	CtxAgentID   ContextKey = "agent_id"
	CtxUserID    ContextKey = "user_id"
	CtxSessionID ContextKey = "session_id"
	CtxRequestID ContextKey = "request_id"
)

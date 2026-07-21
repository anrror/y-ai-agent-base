package types

// StreamEvent represents a single event emitted during a streaming response.
type StreamEvent struct {
	Type      string     `json:"type"`                 // event type, e.g. "chunk", "tool_call", "done", "error"
	Content   string     `json:"content"`              // text payload carried by the event
	Done      bool       `json:"done"`                 // true when the stream is complete
	Error     error      `json:"-"`                    // non-nil when an error occurred (not JSON-serialized)
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // populated when the model requests tool invocations
}

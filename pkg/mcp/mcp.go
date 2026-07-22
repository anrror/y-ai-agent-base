// Package mcp defines an MCP (Model Context Protocol) integration layer
// for the agent framework.
//
// The MCP package provides interfaces and abstractions that allow agents
// to discover and call tools exposed by external MCP servers.
//
// This package ships three built-in transport implementations:
//   - StdioClient — subprocess stdin/stdout MCP transport
//   - SSEClient  — HTTP SSE MCP transport
//   - WSClient   — WebSocket MCP transport
//   - ReconnectClient — wraps any Client with automatic reconnection
//
// In addition to the built-in transports, the Client interface is fully
// public, so host systems can implement custom transports (e.g. WebSocket
// without gorilla/websocket, named pipes, Unix sockets).
//
// Architecture
//
//	┌──────────────────────────────────────────────────┐
//	│                  Agent Chat                       │
//	│  resolve MCP tools from agent config + session    │
//	│  config → append to tool list                     │
//	└──────────────────────┬───────────────────────────┘
//	                       │
//	┌──────────────────────▼───────────────────────────┐
//	│              MCP Registry                         │
//	│  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
//	│  │ Server A │  │ Server B │  │ Server C │       │
//	│  └────┬─────┘  └────┬─────┘  └────┬─────┘       │
//	│       │             │             │              │
//	└───────┼─────────────┼─────────────┼──────────────┘
//	       │             │             │
//	  ┌────▼──┐    ┌────▼──┐    ┌────▼──┐
//	  │ MCP   │    │ MCP   │    │ MCP   │
//	  │Client │    │Client │    │Client │
//	  └────┬──┘    └────┬──┘    └────┬──┘
//	       │             │             │
//	  (stdio)       (HTTP SSE)    (WebSocket)
//
// Usage
//
// The host system creates one or more MCP Server instances, each wrapping
// a Client implementation that connects to an actual MCP server:
//
//	client := mymcp.NewClient("npx -y @modelcontextprotocol/server-filesystem /tmp")
//	server := mcp.NewServer("fs", client)
//
//	reg := mcp.NewRegistry()
//	reg.Add(server)
//
//	reg.Add(mcp.NewServer("weather", myWeatherClient))
//
// Each Agent can reference MCP servers by name in its config. During
// Chat(), the agent resolves the active MCP configuration (agent default
// + per-session overrides) and appends MCP tools to the tool list.
//
// Agent config:
//
//	cfg := agent.Config{
//	    AgentID: "assistant",
//	    MCP: agent.MCPConfig{
//	        Enabled: true,
//	        Servers: []string{"fs", "weather"},
//	    },
//	    ...
//	}
//
// Session override:
//
//	input := types.ChatInput{
//	    Messages: []types.Message{{Role: "user", Content: "hello"}},
//	    MCP: &types.MCPSessionConfig{
//	        Enabled: ptr(true),
//	        Servers: []string{"fs"}, // only filesystem for this session
//	    },
//	}
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Client is the interface that MCP protocol clients must implement.
// The host system provides implementations that connect to actual MCP
// servers via stdio, SSE, WebSocket, or any other transport.
//
// Implementations must be goroutine-safe: ListTools and CallTool may be
// called concurrently from different agent goroutines.
type Client interface {
	// ListTools returns the tools exposed by this MCP server.
	// Results should be stable across calls within a session.
	ListTools(ctx context.Context) ([]*ToolInfo, error)

	// CallTool invokes a tool by name with the given JSON-serialized
	// arguments and returns the result.
	CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error)

	// Close releases resources held by the client (e.g. subprocess,
	// network connection). Idempotent.
	Close() error
}

// ToolInfo describes a tool exposed by an MCP server.
type ToolInfo struct {
	// Name is the tool identifier (e.g. "read_file", "get_weather").
	Name string `json:"name"`

	// Description explains what the tool does.
	Description string `json:"description"`

	// InputSchema is a JSON Schema document describing the tool's
	// parameters. May be nil if the tool accepts no arguments.
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// CallResult is the result of an MCP tool invocation.
type CallResult struct {
	// Content holds the tool's output. Typically a single text item.
	Content []Content `json:"content"`

	// IsError indicates whether the tool execution failed. When true,
	// Content[0].Text contains an error message.
	IsError bool `json:"isError"`
}

// Content is a single content item in an MCP tool response.
type Content struct {
	// Type is the content type (e.g. "text", "image", "resource").
	Type string `json:"type"`

	// Text holds the text content. For non-text types this may be empty
	// or hold a serialised representation.
	Text string `json:"text"`
}

// TextContent returns the concatenation of all text-type content items.
// Returns an empty string when there are no text items.
func (r *CallResult) TextContent() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" || c.Type == "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Error returns an error when IsError is true, or nil otherwise.
func (r *CallResult) Error() error {
	if !r.IsError {
		return nil
	}
	msg := r.TextContent()
	if msg == "" {
		msg = "MCP tool call failed"
	}
	return fmt.Errorf("mcp: %s", msg)
}

// ToolFullName returns the fully-qualified name for an MCP tool in the
// format "server_name/tool_name".
func ToolFullName(serverName, toolName string) string {
	return serverName + "/" + toolName
}

// compile-time interface check.
var _ Client = (*FuncClient)(nil)

// FuncClient is a Client implementation backed by function values.
// Useful in tests and for wrapping existing tool collections without
// implementing the full Client interface.
type FuncClient struct {
	listToolsFn func(ctx context.Context) ([]*ToolInfo, error)
	callToolFn  func(ctx context.Context, name string, args json.RawMessage) (*CallResult, error)
	closeFn     func() error
}

// NewFuncClient creates a FuncClient from the given function values.
// Any function may be nil; the corresponding operation will return
// empty results / an error.
func NewFuncClient(
	listFn func(ctx context.Context) ([]*ToolInfo, error),
	callFn func(ctx context.Context, name string, args json.RawMessage) (*CallResult, error),
	closeFn func() error,
) *FuncClient {
	return &FuncClient{
		listToolsFn: listFn,
		callToolFn:  callFn,
		closeFn:     closeFn,
	}
}

func (f *FuncClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
	if f.listToolsFn == nil {
		return nil, nil
	}
	return f.listToolsFn(ctx)
}

func (f *FuncClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
	if f.callToolFn == nil {
		return nil, fmt.Errorf("mcp: CallTool not implemented")
	}
	return f.callToolFn(ctx, name, args)
}

func (f *FuncClient) Close() error {
	if f.closeFn == nil {
		return nil
	}
	return f.closeFn()
}

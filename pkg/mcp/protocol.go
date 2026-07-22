package mcp

import "encoding/json"

// ---------------------------------------------------------------------------
// MCP Protocol Constants
// ---------------------------------------------------------------------------

// ProtocolVersion is the MCP protocol version used by this implementation.
const ProtocolVersion = "2025-03-26"

// MCP method names.
const (
	methodInitialize    = "initialize"
	methodInitialized   = "notifications/initialized"
	methodListTools     = "tools/list"
	methodCallTool      = "tools/call"
	methodShutdown      = "shutdown"
	methodExit          = "exit"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 wire types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
	return e.Message
}

type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ---------------------------------------------------------------------------
// MCP Initialize
// ---------------------------------------------------------------------------

type initializeParams struct {
	ProtocolVersion string              `json:"protocolVersion"`
	Capabilities    clientCapabilities  `json:"capabilities"`
	ClientInfo      clientInfo          `json:"clientInfo"`
}

type clientCapabilities struct {
	Roots        *rootsCapabilities        `json:"roots,omitempty"`
	Sampling     *samplingCapabilities     `json:"sampling,omitempty"`
	Experimental map[string]any            `json:"experimental,omitempty"`
}

type rootsCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type samplingCapabilities struct{}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string              `json:"protocolVersion"`
	Capabilities    serverCapabilities  `json:"capabilities"`
	ServerInfo      serverInfo          `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools        *toolsCapabilities        `json:"tools,omitempty"`
	Resources    *resourcesCapabilities    `json:"resources,omitempty"`
	Prompts      *promptsCapabilities      `json:"prompts,omitempty"`
	Logging      *loggingCapabilities      `json:"logging,omitempty"`
	Experimental map[string]any            `json:"experimental,omitempty"`
}

type toolsCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type resourcesCapabilities struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type promptsCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type loggingCapabilities struct{}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Other content types (image, resource) are not yet handled.
}

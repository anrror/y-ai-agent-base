package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

// ToolAdapter wraps an MCP tool as a framework tool.Tool.
//
// When the agent's tool execution loop calls Execute, the adapter
// delegates to the MCP server's Client.CallTool and formats the
// result as a string.
//
// The tool name is prefixed with the server name to avoid collisions
// across different MCP servers: "server_name/tool_name".
type ToolAdapter struct {
	serverName string
	info       *ToolInfo
	client     Client
}

// NewToolAdapter creates a ToolAdapter that bridges an MCP tool
// (discovered from a Server) into the framework's tool.Tool interface.
//
// The resulting tool is named "<serverName>/<toolInfo.Name>".
// Call ToolFullName(serverName, toolInfo.Name) to compute the name.
func NewToolAdapter(serverName string, info *ToolInfo, client Client) *ToolAdapter {
	return &ToolAdapter{
		serverName: serverName,
		info:       info,
		client:     client,
	}
}

// Name returns the fully-qualified tool name "serverName/toolName".
func (a *ToolAdapter) Name() string {
	return ToolFullName(a.serverName, a.info.Name)
}

// Description returns the tool's description from the MCP server.
func (a *ToolAdapter) Description() string {
	return a.info.Description
}

// Schema returns the tool's input JSON Schema.
func (a *ToolAdapter) Schema() json.RawMessage {
	return a.info.InputSchema
}

// Execute calls the MCP tool via the server client and returns the
// text result. When CallResult.IsError is true, the returned error
// contains the error message from the MCP server.
func (a *ToolAdapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	result, err := a.client.CallTool(ctx, a.info.Name, args)
	if err != nil {
		return "", fmt.Errorf("mcp: %s/%s: %w", a.serverName, a.info.Name, err)
	}
	if err := result.Error(); err != nil {
		return "", fmt.Errorf("mcp: %s/%s: %w", a.serverName, a.info.Name, err)
	}
	return result.TextContent(), nil
}

// compile-time interface checks.
var (
	_ tool.Tool = (*ToolAdapter)(nil)
)

// ServerTools converts all tools from a Server into ToolAdapter instances.
// Returns an error if the server's Client fails to list tools.
func ServerTools(server *Server) ([]tool.Tool, error) {
	if server.Client == nil {
		return nil, nil
	}

	infos, err := server.Client.ListTools(context.Background())
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools from %q: %w", server.Name, err)
	}

	tools := make([]tool.Tool, 0, len(infos))
	for _, info := range infos {
		tools = append(tools, NewToolAdapter(server.Name, info, server.Client))
	}
	return tools, nil
}

// ResolveTools resolves the effective set of MCP tools for a given
// server list from the registry.
//
// serverNames specifies which servers to include. An empty slice means
// "all servers in the registry".
//
// This is the primary function used by Agent to resolve MCP tools
// during Chat() execution.
func ResolveTools(registry *Registry, serverNames []string) ([]tool.Tool, error) {
	if registry == nil {
		return nil, nil
	}

	// When serverNames is empty, include ALL servers.
	if len(serverNames) == 0 {
		serverNames = registry.ServerNames()
	}

	var allTools []tool.Tool
	for _, name := range serverNames {
		server := registry.Get(name)
		if server == nil {
			return nil, fmt.Errorf("mcp: server %q not found in registry", name)
		}
		tools, err := ServerTools(server)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, tools...)
	}
	return allTools, nil
}

package mcp

import "fmt"

// Server represents a named MCP server connection.
//
// A Server pairs a unique name with a Client that connects to an actual
// MCP server via stdio, HTTP SSE, WebSocket, etc.
//
// The host system creates Server instances and registers them in a
// Registry. Agents reference servers by name in their MCPConfig.
type Server struct {
	// Name is a unique identifier for this server (e.g. "filesystem",
	// "weather", "database"). Used in agent config and tool naming.
	Name string

	// Client is the MCP protocol client that communicates with the
	// actual MCP server.
	Client Client
}

// NewServer creates a named MCP Server.
// The name must be non-empty; it is used as the prefix in fully-qualified
// tool names ("<name>/<tool>").
func NewServer(name string, client Client) *Server {
	if name == "" {
		name = "mcp"
	}
	return &Server{Name: name, Client: client}
}

// Validate checks that the server has a non-empty name and a non-nil client.
func (s *Server) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("mcp: server name is required")
	}
	if s.Client == nil {
		return fmt.Errorf("mcp: server %q: client is nil", s.Name)
	}
	return nil
}

// Close releases the client connection.
func (s *Server) Close() error {
	if s.Client != nil {
		return s.Client.Close()
	}
	return nil
}

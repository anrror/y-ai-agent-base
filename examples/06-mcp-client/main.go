// Example 06: MCP Client — demonstrates connecting to MCP servers
// via stdio, SSE, and WebSocket transports.
//
// This example shows how to:
//  1. Create StdioClient, SSEClient, and WSClient instances.
//  2. Use ReconnectClient for automatic reconnection.
//  3. Wrap clients in mcp.Server and register them in a Registry.
//  4. Resolve and call MCP tools through the framework's ToolAdapter.
//
// Run:
//
//	go run ./examples/06-mcp-client/ [transport]
//
// Where transport is one of: "stdio" (default), "sse", "ws", "reconnect".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/mcp"
	"github.com/gorilla/websocket"
)

func main() {
	transport := "stdio"
	if len(os.Args) > 1 {
		transport = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch transport {
	case "stdio":
		demoStdio(ctx)
	case "sse":
		demoSSE(ctx)
	case "ws":
		demoWS(ctx)
	case "reconnect":
		demoReconnect(ctx)
	default:
		fmt.Fprintf(os.Stderr, "unknown transport %q; use stdio/sse/ws/reconnect\n", transport)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Stdio demo — connects to a mock MCP stdio server.
// ---------------------------------------------------------------------------
func demoStdio(ctx context.Context) {
	// In production, use a real command like:
	//   client := mcp.NewStdioClient("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
	client := mcp.NewStdioClient("go", "run",
		"github.com/anrror/y-ai-agent-base/pkg/mcp/testdata/mcp-stdio-mock")
	if err := client.Start(ctx); err != nil {
		log.Fatalf("stdio start: %v", err)
	}
	defer client.Close()

	listAndCall(ctx, client, "stdio")
}

// ---------------------------------------------------------------------------
// SSE demo — connects to a mock MCP SSE server.
// ---------------------------------------------------------------------------
func demoSSE(ctx context.Context) {
	srv := newSSEMockServer()
	defer srv.Close()

	client := mcp.NewSSEClient(srv.URL(), nil)
	if err := client.Start(ctx); err != nil {
		log.Fatalf("sse start: %v", err)
	}
	defer client.Close()

	listAndCall(ctx, client, "sse")
}

// ---------------------------------------------------------------------------
// WebSocket demo — connects to a mock MCP WS server.
// ---------------------------------------------------------------------------
func demoWS(ctx context.Context) {
	srv := newWSMockServer()
	defer srv.Close()

	client := mcp.NewWSClient(srv.WSURL, nil, nil)
	if err := client.Start(ctx); err != nil {
		log.Fatalf("ws start: %v", err)
	}
	defer client.Close()

	listAndCall(ctx, client, "ws")
}

// ---------------------------------------------------------------------------
// Reconnect demo — wraps a client with auto-reconnection.
// ---------------------------------------------------------------------------
func demoReconnect(ctx context.Context) {
	base := mcp.NewFuncClient(
		func(_ context.Context) ([]*mcp.ToolInfo, error) {
			return []*mcp.ToolInfo{
				{Name: "reliable_tool", Description: "A tool that always works"},
			}, nil
		},
		func(_ context.Context, name string, _ json.RawMessage) (*mcp.CallResult, error) {
			return &mcp.CallResult{
				Content: []mcp.Content{{Type: "text", Text: "result from " + name}},
			}, nil
		},
		nil,
	)

	rc := mcp.NewReconnectClient(base, nil,
		mcp.WithMaxRetries(3),
		mcp.WithBaseDelay(100*time.Millisecond),
		mcp.WithMaxDelay(time.Second),
		mcp.WithStateHook(func(s mcp.ConnState) {
			fmt.Printf("  [reconnect] state: %s\n", s)
		}),
	)
	defer rc.Close()

	listAndCall(ctx, rc, "reconnect")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func listAndCall(ctx context.Context, client mcp.Client, label string) {
	tools, err := client.ListTools(ctx)
	if err != nil {
		log.Fatalf("%s ListTools: %v", label, err)
	}
	fmt.Printf("[%s] tools: %d\n", label, len(tools))
	for _, t := range tools {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}

	if len(tools) > 0 {
		result, err := client.CallTool(ctx, tools[0].Name, json.RawMessage(`{}`))
		if err != nil {
			log.Fatalf("%s CallTool: %v", label, err)
		}
		fmt.Printf("[%s] %s -> %s\n", label, tools[0].Name, result.TextContent())
	}
}

// ---------------------------------------------------------------------------
// Mock SSE server for the demo.
// ---------------------------------------------------------------------------
type sseMock struct {
	server    *httptest.Server
	responses chan []byte
	closeCh   chan struct{}
}

func newSSEMockServer() *sseMock {
	s := &sseMock{
		responses: make(chan []byte, 32),
		closeCh:   make(chan struct{}),
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleSSE(w, r)
		} else if r.Method == http.MethodPost {
			s.handlePost(w, r)
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	return s
}

func (s *sseMock) URL() string { return s.server.URL }

func (s *sseMock) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	fmt.Fprintf(w, "event: endpoint\ndata: /message\n\n")
	flusher.Flush()

	for {
		select {
		case resp := <-s.responses:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.closeCh:
			return
		}
	}
}

func (s *sseMock) handlePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp := buildMockResponse(req.ID, req.Method)
	data, _ := json.Marshal(resp)
	select {
	case s.responses <- data:
	default:
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *sseMock) Close() {
	close(s.closeCh)
	s.server.Close()
}

// ---------------------------------------------------------------------------
// Mock WebSocket server for the demo.
// ---------------------------------------------------------------------------
type wsMock struct {
	server *httptest.Server
	WSURL  string
}

func newWSMockServer() *wsMock {
	s := &wsMock{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			resp := buildMockResponse(req.ID, req.Method)
			data, _ := json.Marshal(resp)
			_ = conn.WriteMessage(websocket.TextMessage, data)
		}
	}))
	s.WSURL = "ws" + s.server.URL[len("http"):]
	return s
}

func (s *wsMock) Close() {
	s.server.Close()
}

func buildMockResponse(id int, method string) map[string]any {
	switch method {
	case "initialize":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "demo-mock", "version": "1.0.0"},
			},
		}
	case "tools/list":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "greet", "description": "Says hello", "inputSchema": map[string]any{"type": "object"}},
				},
			},
		}
	case "tools/call":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "Hello from MCP!"}},
				"isError": false,
			},
		}
	default:
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error":   map[string]any{"code": -32601, "message": "Method not found"},
		}
	}
}

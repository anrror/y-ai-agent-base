package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/mcp"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// StdioClient tests
// ---------------------------------------------------------------------------

var mockServerBinary string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}

	mockServerBinary = filepath.Join(tmpDir, "mcp-stdio-mock"+exeSuffix())
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "go not found: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command("go", "build", "-o", mockServerBinary,
		"github.com/anrror/y-ai-agent-base/pkg/mcp/testdata/mcp-stdio-mock")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build mock server: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func TestStdioClient_ListTools(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))

	tools, err := client.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 2)

	assert.Equal(t, "echo", tools[0].Name)
	assert.Equal(t, "Echoes back the input", tools[0].Description)
	assert.NotNil(t, tools[0].InputSchema)

	assert.Equal(t, "add", tools[1].Name)
	assert.Equal(t, "Adds two numbers", tools[1].Description)

	require.NoError(t, client.Close())
}

func TestStdioClient_CallTool(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))

	result, err := client.CallTool(context.Background(), "echo",
		json.RawMessage(`{"message":"hello"}`))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, `{"message":"hello"}`, result.TextContent())

	result, err = client.CallTool(context.Background(), "add",
		json.RawMessage(`{"a":3,"b":4}`))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "7", result.TextContent())

	require.NoError(t, client.Close())
}

func TestStdioClient_CallTool_Error(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))

	result, err := client.CallTool(context.Background(), "nonexistent", nil)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.TextContent(), "unknown tool")

	require.NoError(t, client.Close())
}

func TestStdioClient_ContextCancellation(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := client.ListTools(ctx)
	require.Error(t, err)
	require.NoError(t, client.Close())
}

func TestStdioClient_EagerInit(t *testing.T) {
	client := mcp.NewStdioClientOpts(mockServerBinary, nil, mcp.WithStdioLazyInit(false))
	require.NoError(t, client.Start(context.Background()))

	tools, err := client.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "echo", tools[0].Name)

	require.NoError(t, client.Close())
}

func TestStdioClient_WontStartTwice(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))
	require.Error(t, client.Start(context.Background()))
	require.NoError(t, client.Close())
}

// ---------------------------------------------------------------------------
// SSEClient tests
// ---------------------------------------------------------------------------

// sseMCPServer is a mock MCP server over SSE.
//
// It runs a single httptest.Server with a combined handler:
//   - GET / → SSE event stream (sends "endpoint" event, then "message"
//     events as requests come in)
//   - POST /message → receives JSON-RPC requests, dispatches responses
//     as SSE "message" events on the stream opened by GET /.
type sseMCPServer struct {
	server    *httptest.Server
	responses chan []byte // POST handler → SSE handler
	once      sync.Once
	closeCh   chan struct{}
}

func newSSEMCPServer() *sseMCPServer {
	s := &sseMCPServer{
		responses: make(chan []byte, 32),
		closeCh:   make(chan struct{}),
	}

	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleSSE(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/message") {
			s.handlePost(w, r)
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))

	return s
}

func (s *sseMCPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Send endpoint event.
	fmt.Fprintf(w, "event: endpoint\ndata: /message\n\n")
	flusher.Flush()

	for {
		select {
		case resp, ok := <-s.responses:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
			flusher.Flush()

		case <-ctx.Done():
			return

		case <-s.closeCh:
			return
		}
	}
}

func (s *sseMCPServer) handlePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int              `json:"id"`
		Method string           `json:"method"`
		Params json.RawMessage  `json:"params,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Build response.
	resp := s.buildResponse(req.ID, req.Method)
	respData, _ := json.Marshal(resp)

	// Queue it on the SSE stream.
	select {
	case s.responses <- respData:
	default:
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *sseMCPServer) buildResponse(id int, method string) map[string]any {
	switch method {
	case "initialize":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo": map[string]any{
					"name":    "sse-mock-server",
					"version": "1.0.0",
				},
			},
		}
	case "tools/list":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]any{
				"tools": []map[string]any{
					{
						"name":        "sse_greet",
						"description": "Greets the user",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		}
	case "tools/call":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Hello from SSE!"},
				},
				"isError": false,
			},
		}
	default:
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		}
	}
}

func (s *sseMCPServer) Close() {
	s.once.Do(func() {
		close(s.closeCh)
		s.server.Close()
	})
}

func (s *sseMCPServer) URL() string {
	return s.server.URL
}

func TestSSEClient_ListTools(t *testing.T) {
	srv := newSSEMCPServer()
	defer srv.Close()

	client := mcp.NewSSEClient(srv.URL(), nil)
	require.NoError(t, client.Start(context.Background()))

	tools, err := client.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "sse_greet", tools[0].Name)
	assert.Equal(t, "Greets the user", tools[0].Description)
	assert.NotNil(t, tools[0].InputSchema)

	require.NoError(t, client.Close())
}

func TestSSEClient_CallTool(t *testing.T) {
	srv := newSSEMCPServer()
	defer srv.Close()

	client := mcp.NewSSEClient(srv.URL(), nil)
	require.NoError(t, client.Start(context.Background()))

	result, err := client.CallTool(context.Background(), "sse_greet",
		json.RawMessage(`{"name":"world"}`))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "Hello from SSE!", result.TextContent())

	require.NoError(t, client.Close())
}

func TestSSEClient_ContextCancellation(t *testing.T) {
	srv := newSSEMCPServer()
	defer srv.Close()

	client := mcp.NewSSEClient(srv.URL(), nil)
	require.NoError(t, client.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.ListTools(ctx)
	require.Error(t, err)
	require.NoError(t, client.Close())
}

// ---------------------------------------------------------------------------
// MCP integration test: StdioClient → Server → Registry → ToolAdapter
// ---------------------------------------------------------------------------

func TestMCPIntegration_Stdio(t *testing.T) {
	client := mcp.NewStdioClient(mockServerBinary)
	require.NoError(t, client.Start(context.Background()))

	server := mcp.NewServer("mock", client)
	reg := mcp.NewRegistry()
	require.NoError(t, reg.Add(server))

	tools, err := mcp.ResolveTools(reg, nil)
	require.NoError(t, err)
	require.Len(t, tools, 2)

	assert.Equal(t, "mock/echo", tools[0].Name())
	assert.Equal(t, "mock/add", tools[1].Name())

	result, err := tools[1].Execute(context.Background(),
		json.RawMessage(`{"a":10,"b":20}`))
	require.NoError(t, err)
	assert.Equal(t, "30", result)

	require.NoError(t, reg.Close())
}

// ---------------------------------------------------------------------------
// WSClient tests
// ---------------------------------------------------------------------------

// wsMCPServer is a mock MCP server over WebSocket.
// It accepts a single WebSocket connection, then reads JSON-RPC
// requests and writes responses as text frames.
type wsMCPServer struct {
	server *httptest.Server
	url    string
	upgrader websocket.Upgrader
	mu       sync.Mutex
}

func newWSMCPServer() *wsMCPServer {
	s := &wsMCPServer{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handleConn))
	s.url = "ws" + strings.TrimPrefix(s.server.URL, "http")
	return s
}

func (s *wsMCPServer) handleConn(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var req struct {
			ID     int              `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params,omitempty"`
		}
		if err := json.Unmarshal(message, &req); err != nil {
			continue
		}

		resp := s.buildResponse(req.ID, req.Method)
		respData, _ := json.Marshal(resp)
		_ = conn.WriteMessage(websocket.TextMessage, respData)
	}
}

func (s *wsMCPServer) buildResponse(id int, method string) map[string]any {
	switch method {
	case "initialize":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "ws-mock", "version": "1.0.0"},
			},
		}
	case "tools/list":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "ws_echo", "description": "WS echo", "inputSchema": map[string]any{"type": "object"}},
				},
			},
		}
	case "tools/call":
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ws done"}},
				"isError": false,
			},
		}
	default:
		return map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": -32601, "message": "Method not found"},
		}
	}
}

func (s *wsMCPServer) Close() {
	s.server.Close()
}

func TestWSClient_ListTools(t *testing.T) {
	srv := newWSMCPServer()
	defer srv.Close()

	client := mcp.NewWSClient(srv.url, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	tools, err := client.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "ws_echo", tools[0].Name)

	require.NoError(t, client.Close())
}

func TestWSClient_CallTool(t *testing.T) {
	srv := newWSMCPServer()
	defer srv.Close()

	client := mcp.NewWSClient(srv.url, nil, nil)
	require.NoError(t, client.Start(context.Background()))

	result, err := client.CallTool(context.Background(), "ws_echo", nil)
	require.NoError(t, err)
	assert.Equal(t, "ws done", result.TextContent())

	require.NoError(t, client.Close())
}

// ---------------------------------------------------------------------------
// ReconnectClient tests
// ---------------------------------------------------------------------------

func TestReconnectClient_RetriesOnFailure(t *testing.T) {
	var callCount int
	client := mcp.NewFuncClient(
		func(_ context.Context) ([]*mcp.ToolInfo, error) {
			callCount++
			if callCount < 3 {
				return nil, mcp.ErrConnectionClosed
			}
			return []*mcp.ToolInfo{{Name: "ok"}}, nil
		},
		nil, nil,
	)

	rc := mcp.NewReconnectClient(client, nil,
		mcp.WithMaxRetries(5),
		mcp.WithBaseDelay(time.Millisecond*10),
		mcp.WithMaxDelay(time.Millisecond*50),
	)

	tools, err := rc.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "ok", tools[0].Name)
	assert.Equal(t, 3, callCount)
	require.NoError(t, rc.Close())
}

func TestReconnectClient_NonRetriableError(t *testing.T) {
	client := mcp.NewFuncClient(
		func(_ context.Context) ([]*mcp.ToolInfo, error) {
			return nil, assert.AnError
		},
		nil, nil,
	)

	rc := mcp.NewReconnectClient(client, nil,
		mcp.WithMaxRetries(3),
		mcp.WithBaseDelay(time.Millisecond),
	)

	_, err := rc.ListTools(context.Background())
	require.Error(t, err)
	require.NoError(t, rc.Close())
}

func TestReconnectClient_ExhaustsRetries(t *testing.T) {
	var callCount int
	client := mcp.NewFuncClient(
		func(_ context.Context) ([]*mcp.ToolInfo, error) {
			callCount++
			return nil, mcp.ErrConnectionClosed
		},
		nil, nil,
	)

	rc := mcp.NewReconnectClient(client, nil,
		mcp.WithMaxRetries(2),
		mcp.WithBaseDelay(time.Millisecond),
		mcp.WithMaxDelay(time.Millisecond*5),
	)

	_, err := rc.ListTools(context.Background())
	require.Error(t, err)
	assert.True(t, callCount >= 2)
	require.NoError(t, rc.Close())
}

func TestReconnectClient_StateTransition(t *testing.T) {
	var states []mcp.ConnState
	var mu sync.Mutex

	client := mcp.NewFuncClient(
		func(_ context.Context) ([]*mcp.ToolInfo, error) {
			return []*mcp.ToolInfo{{Name: "ok"}}, nil
		},
		nil, nil,
	)

	rc := mcp.NewReconnectClient(client, nil,
		mcp.WithStateHook(func(s mcp.ConnState) {
			mu.Lock()
			states = append(states, s)
			mu.Unlock()
		}),
	)

	assert.Equal(t, mcp.StateConnected, rc.State())

	require.NoError(t, rc.Close())
	assert.Equal(t, mcp.StateDisconnected, rc.State())

	mu.Lock()
	require.Contains(t, states, mcp.StateDisconnected)
	mu.Unlock()
}

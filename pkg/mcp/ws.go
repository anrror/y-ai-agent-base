package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSClient implements the MCP Client interface over WebSocket transport.
//
// It connects to an MCP server via ws:// or wss:// URL and communicates
// using JSON-RPC 2.0 over WebSocket text frames.
//
// Usage:
//
//	client := mcp.NewWSClient("ws://localhost:3001/mcp", nil)
//	server := mcp.NewServer("remote", client)
//
// The client supports TLS (wss://) and custom HTTP headers via the
// dialer's TLS configuration.
type WSClient struct {
	url    string
	dialer *websocket.Dialer
	header http.Header

	conn   *websocket.Conn
	connMu sync.RWMutex

	mu      sync.Mutex
	seq     int
	pending map[int]chan<- jsonRPCResponse
	closed  bool

	initOnce sync.Once
	initErr  error

	readDone chan struct{}
}

// NewWSClient creates a WSClient connected to the given WebSocket URL.
// When dialer is nil, a default dialer is used.
// When header is non-nil, it is sent with the initial HTTP request
// (useful for auth tokens).
func NewWSClient(url string, dialer *websocket.Dialer, header http.Header) *WSClient {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	return &WSClient{
		url:      url,
		dialer:   dialer,
		header:   header,
		pending:  make(map[int]chan<- jsonRPCResponse),
		readDone: make(chan struct{}),
	}
}

// Start connects to the WebSocket server and begins the read loop.
// If lazyInit is enabled (default), the MCP initialize handshake happens
// on the first ListTools / CallTool call.
func (c *WSClient) Start(ctx context.Context) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.closed {
		return ErrShutdown
	}
	if c.conn != nil {
		return fmt.Errorf("mcp ws: already started")
	}

	conn, resp, err := c.dialer.DialContext(ctx, c.url, c.header)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return fmt.Errorf("mcp ws: dial %q: %w", c.url, err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	c.conn = conn
	go c.readLoop(ctx)

	return nil
}

// readLoop reads WebSocket text frames and dispatches JSON-RPC responses.
func (c *WSClient) readLoop(ctx context.Context) {
	defer close(c.readDone)

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			// Connection closed or error.
			c.mu.Lock()
			c.closed = true
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue // malformed frame — skip
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok && ch != nil {
			ch <- resp
		}
		// Notifications (no matching pending) are dropped.
	}
}

// sendRequest sends a JSON-RPC request and returns the response.
func (c *WSClient) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return c.sendRawRequest(ctx, method, params)
}

// sendRawRequest sends a JSON-RPC request without auto-initialization.
func (c *WSClient) sendRawRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrConnectionClosed
	}
	c.seq++
	id := c.seq
	ch := make(chan jsonRPCResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var cleanupOnce sync.Once
	cleanup := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		cleanupOnce.Do(cleanup)
		return nil, ErrConnectionClosed
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp ws: write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, ErrConnectionClosed
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: %s: %w", method, resp.Error)
		}
		return resp.Result, nil

	case <-ctx.Done():
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp: %s: %w", method, ctx.Err())
	}
}

// ensureInitialized performs the MCP initialize handshake once.
func (c *WSClient) ensureInitialized(ctx context.Context) error {
	var initErr error
	c.initOnce.Do(func() {
		initErr = c.doInitialize(ctx)
	})
	return initErr
}

func (c *WSClient) doInitialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	params := initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    clientCapabilities{},
		ClientInfo: clientInfo{
			Name:    "y-ai-agent-base",
			Version: "0.1.0",
		},
	}

	_, err := c.sendRawRequest(initCtx, methodInitialize, params)
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}

	// Send initialized notification (fire-and-forget).
	notif := jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  methodInitialized,
	}
	if notifData, _ := json.Marshal(notif); notifData != nil {
		c.connMu.RLock()
		conn := c.conn
		c.connMu.RUnlock()
		if conn != nil {
			_ = conn.WriteMessage(websocket.TextMessage, notifData)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// mcp.Client implementation
// ---------------------------------------------------------------------------

// ListTools returns the tools exposed by the MCP server.
func (c *WSClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
	data, err := c.sendRequest(ctx, methodListTools, nil)
	if err != nil {
		return nil, err
	}

	var result listToolsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal ListTools result: %w", err)
	}

	tools := make([]*ToolInfo, len(result.Tools))
	for i := range result.Tools {
		t := result.Tools[i]
		tools[i] = &ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return tools, nil
}

// CallTool invokes a tool by name with JSON-serialized arguments.
func (c *WSClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
	params := callToolParams{
		Name:      name,
		Arguments: args,
	}

	data, err := c.sendRequest(ctx, methodCallTool, params)
	if err != nil {
		return nil, err
	}

	var result callToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal CallTool result: %w", err)
	}

	content := make([]Content, len(result.Content))
	for i, ci := range result.Content {
		content[i] = Content{
			Type: ci.Type,
			Text: ci.Text,
		}
	}

	return &CallResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// Close closes the WebSocket connection.
func (c *WSClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// Drain pending.
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()

	c.connMu.Lock()
	if c.conn != nil {
		_ = c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connMu.Unlock()

	return nil
}

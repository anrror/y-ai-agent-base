package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SSEClient implements the MCP Client interface over HTTP SSE transport.
//
// It connects to an MCP server via Server-Sent Events, receives a session
// endpoint, and sends JSON-RPC requests via HTTP POST to that endpoint.
//
// Usage:
//
//	client := mcp.NewSSEClient("http://localhost:3001/mcp", nil)
//	server := mcp.NewServer("remote", client)
type SSEClient struct {
	baseURL    string
	httpClient *http.Client
	sseURL     string // POST endpoint for JSON-RPC

	// Communication channels
	msgCh  chan jsonRPCResponse
	doneCh chan struct{}

	mu             sync.Mutex
	seq            int
	pending        map[int]chan<- jsonRPCResponse
	closed         bool
	onceEndpoint   sync.Once

	initOnce sync.Once
	initErr  error
}

// NewSSEClient creates an SSEClient that connects to an MCP server via HTTP SSE.
// The baseURL is the SSE endpoint (e.g. "http://localhost:3001/mcp" or "/sse").
// When httpClient is nil, http.DefaultClient is used.
func NewSSEClient(baseURL string, httpClient *http.Client) *SSEClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &SSEClient{
		baseURL:    baseURL,
		httpClient: httpClient,
		msgCh:      make(chan jsonRPCResponse, 64),
		doneCh:     make(chan struct{}),
		pending:    make(map[int]chan<- jsonRPCResponse),
	}
}

// Start connects to the SSE endpoint and begins processing events.
// It blocks until the SSE connection is established and the session
// endpoint is received. The initialization handshake happens lazily.
func (c *SSEClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("mcp sse: client is closed")
	}
	c.mu.Unlock()

	// Connect to SSE endpoint.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return fmt.Errorf("mcp sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("mcp sse: unexpected status %d", resp.StatusCode)
	}

	// Read SSE events in background.
	go c.readSSE(ctx, resp.Body)

	// Wait for the endpoint event to get the POST URL.
	// Use a short timeout for the initial endpoint event.
	endpointCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	select {
	case <-c.doneCh:
		// SSE endpoint event received.
	case <-endpointCtx.Done():
		return fmt.Errorf("mcp sse: timeout waiting for endpoint event")
	}

	return nil
}

// readSSE reads and parses SSE events from the response body.
// SSE format:
//
//	event: <type>
//	data: <json>
//
// (blank line separates events)
func (c *SSEClient) readSSE(ctx context.Context, body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// Max SSE line length: 10MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var eventType string
	var dataBuf strings.Builder

	flushEvent := func() {
		if eventType == "" && dataBuf.Len() == 0 {
			return
		}
		data := dataBuf.String()
		dataBuf.Reset()

		switch eventType {
		case "endpoint":
			// The endpoint data contains the POST URL for JSON-RPC.
			c.mu.Lock()
			c.sseURL = strings.TrimSpace(data)
			c.mu.Unlock()
			// Signal that we've received the endpoint (only once).
			c.onceEndpoint.Do(func() {
				close(c.doneCh)
			})

		case "message":
			// JSON-RPC response.
			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				// Malformed response — skip.
				break
			}
			c.dispatchResponse(resp)
		}
		eventType = ""
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flushEvent()
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			flushEvent()
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if dataBuf.Len() > 0 {
				dataBuf.WriteString("\n")
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
		// Other SSE fields (id, retry) are ignored.
	}

	// Flush remaining event.
	flushEvent()
}

// dispatchResponse routes a JSON-RPC response to the matching pending request.
func (c *SSEClient) dispatchResponse(resp jsonRPCResponse) {
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	if ok {
		delete(c.pending, resp.ID)
	}
	c.mu.Unlock()

	if ok && ch != nil {
		ch <- resp
	}
}

// sendRequest sends a JSON-RPC request via HTTP POST and returns the response.
func (c *SSEClient) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return c.sendRawRequest(ctx, method, params)
}

// sendRawRequest sends a JSON-RPC request without auto-initialization.
func (c *SSEClient) sendRawRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Get the SSE POST URL.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp sse: connection closed")
	}
	sseURL := c.sseURL
	if sseURL == "" {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp sse: no endpoint URL received yet")
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

	body, err := json.Marshal(req)
	if err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	// Resolve full endpoint URL.
	endpointURL := c.resolveEndpoint(sseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp sse: create POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp sse: POST: %w", err)
	}
	httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusAccepted {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp sse: POST returned %d", httpResp.StatusCode)
	}

	// Wait for response on SSE stream or context cancellation.
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp sse: connection closed while waiting for response")
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

// resolveEndpoint resolves the SSE POST endpoint URL relative to the base URL.
func (c *SSEClient) resolveEndpoint(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	// Relative URL — resolve against the base URL.
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return endpoint
	}
	ref, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	return base.ResolveReference(ref).String()
}

// ensureInitialized performs the MCP initialize handshake once.
func (c *SSEClient) ensureInitialized(ctx context.Context) error {
	var initErr error
	c.initOnce.Do(func() {
		initErr = c.doInitialize(ctx)
	})
	return initErr
}

func (c *SSEClient) doInitialize(ctx context.Context) error {
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
		httpReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			c.resolveEndpoint(c.sseURL), bytes.NewReader(notifData))
		if httpReq != nil {
			httpReq.Header.Set("Content-Type", "application/json")
			resp, _ := c.httpClient.Do(httpReq)
			if resp != nil {
				resp.Body.Close()
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// mcp.Client implementation
// ---------------------------------------------------------------------------

// ListTools returns the tools exposed by the MCP server.
func (c *SSEClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
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
func (c *SSEClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
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

// Close closes the SSE connection.
func (c *SSEClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// Drain pending channels.
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	return nil
}

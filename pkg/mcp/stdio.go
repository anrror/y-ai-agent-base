package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// StdioClient implements the MCP Client interface over stdio transport.
//
// It spawns a subprocess (e.g. "npx", "node", "python") and communicates
// via stdin/stdout using newline-delimited JSON-RPC 2.0 messages per the
// MCP specification.
//
// Usage:
//
//	client := mcp.NewStdioClient("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
//
//	server := mcp.NewServer("fs", client)
//	reg := mcp.NewRegistry()
//	reg.Add(server)
//
//	lazyInit controls whether initialization happens on first use (lazyInit=true)
//	or immediately (lazyInit=false, the default).
type StdioClient struct {
	cmd      *exec.Cmd
	args     []string
	lazyInit bool

	stdin  io.WriteCloser
	scanner *bufio.Scanner
	cancel context.CancelFunc

	mu      sync.Mutex
	seq     int
	pending map[int]chan<- jsonRPCResponse
	closed  bool

	initOnce sync.Once
	initMu   sync.RWMutex
	initDone bool
	initErr  error
}

// NewStdioClient creates a StdioClient that spawns the given command.
// The command is executed via exec.CommandContext; the process is killed
// when Close() is called or context is cancelled.
//
// By default, initialization (MCP handshake) happens synchronously on the
// first ListTools or CallTool call (lazy). Pass StdioLazyInit(false) to
// initialize eagerly when Start is called.
// StdioOption is a functional option for StdioClient.
type StdioOption func(*StdioClient)

// WithStdioLazyInit sets whether MCP initialization is deferred until first use.
// Pass false to initialize eagerly when Start is called.
func WithStdioLazyInit(lazy bool) StdioOption {
	return func(c *StdioClient) {
		c.lazyInit = lazy
	}
}

// StdioLazyInit is a deprecated alias; use WithStdioLazyInit.
func StdioLazyInit(lazy bool) StdioOption {
	return WithStdioLazyInit(lazy)
}

// NewStdioClient creates a StdioClient that spawns the given command.
// The command is executed via exec.CommandContext; the process is killed
// when Close() is called or context is cancelled.
//
// By default, initialization (MCP handshake) happens synchronously on the
// first ListTools or CallTool call (lazy). Pass WithStdioLazyInit(false) to
// initialize eagerly when Start is called.
func NewStdioClient(command string, args ...string) *StdioClient {
	return &StdioClient{
		args:     append([]string{command}, args...),
		pending:  make(map[int]chan<- jsonRPCResponse),
		lazyInit: true,
	}
}

// NewStdioClientOpts creates a StdioClient with additional options.
func NewStdioClientOpts(command string, args []string, opts ...StdioOption) *StdioClient {
	c := NewStdioClient(command, args...)
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start spawns the subprocess and begins the JSON-RPC message loop.
// When lazyInit is false, Start also performs the MCP initialize handshake.
func (c *StdioClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("mcp stdio: client is closed")
	}
	if c.cmd != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp stdio: already started")
	}
	c.mu.Unlock()

	// Build command from args.
	if len(c.args) == 0 {
		return fmt.Errorf("mcp stdio: no command specified")
	}
	cmdCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.cmd = exec.CommandContext(cmdCtx, c.args[0], c.args[1:]...)

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	c.scanner = bufio.NewScanner(stdout)
	// Max token size: 10MB for large MCP responses.
	c.scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	if err := c.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("mcp stdio: start %q: %w", c.args[0], err)
	}

	// Read loop in background goroutine.
	go c.readLoop()

	// Eager initialization when requested.
	if !c.lazyInit {
		if err := c.ensureInitialized(ctx); err != nil {
			return err
		}
	}

	return nil
}

// readLoop reads JSON-RPC responses from stdout and dispatches them
// to the pending request channels.
func (c *StdioClient) readLoop() {
	for c.scanner.Scan() {
		line := c.scanner.Text()
		if line == "" {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Could be a notification (no ID) or malformed.
			// Notifications are silently dropped in this simple implementation.
			continue
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
	}

	// Scanner ended — process died or stream closed.
	c.mu.Lock()
	c.closed = true
	// Drain pending channels to prevent goroutine leaks.
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// sendRequest sends a JSON-RPC request and returns the response.
func (c *StdioClient) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return c.sendRawRequest(ctx, method, params)
}

// sendRawRequest sends a JSON-RPC request without automatic initialization.
func (c *StdioClient) sendRawRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp stdio: connection closed")
	}
	c.seq++
	id := c.seq

	ch := make(chan jsonRPCResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	// Cleanup on failure.
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

	// Write newline-delimited JSON to stdin.
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		cleanupOnce.Do(cleanup)
		return nil, fmt.Errorf("mcp stdio: write: %w", err)
	}

	// Wait for response or context cancellation.
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp stdio: connection closed while waiting for response")
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
func (c *StdioClient) ensureInitialized(ctx context.Context) error {
	var initErr error
	c.initOnce.Do(func() {
		initErr = c.doInitialize(ctx)
	})
	return initErr
}

func (c *StdioClient) doInitialize(ctx context.Context) error {
	// Use a short timeout for initialization.
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
		c.initMu.Lock()
		c.initErr = err
		c.initMu.Unlock()
		return fmt.Errorf("mcp: initialize: %w", err)
	}

	// Send initialized notification (fire-and-forget, no error handling needed).
	notif := jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  methodInitialized,
	}
	if notifData, _ := json.Marshal(notif); notifData != nil {
		notifData = append(notifData, '\n')
		_, _ = c.stdin.Write(notifData)
	}

	c.initMu.Lock()
	c.initDone = true
	c.initMu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// mcp.Client implementation
// ---------------------------------------------------------------------------

// ListTools returns the tools exposed by the MCP server.
func (c *StdioClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
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
func (c *StdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
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

// Close terminates the subprocess and cleans up resources.
func (c *StdioClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Cancel the context to kill the subprocess.
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for the process and discard errors — we are force-killing it,
	// so the exit code / TerminateProcess errors are irrelevant.
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}

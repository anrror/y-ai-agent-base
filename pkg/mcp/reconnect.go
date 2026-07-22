package mcp

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"
)

// ConnState represents the connection state of a ReconnectClient.
type ConnState int

const (
	// StateConnected indicates the client is connected and operational.
	StateConnected ConnState = iota
	// StateReconnecting indicates the client is attempting to reconnect.
	StateReconnecting
	// StateDisconnected indicates the client is permanently disconnected.
	StateDisconnected
)

func (s ConnState) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// ReconnectClient wraps an MCP Client with automatic reconnection logic.
//
// When a retriable error is detected (connection lost, temporary network
// failure), the ReconnectClient attempts to re-establish the connection
// using exponential backoff. Non-retriable errors are passed through.
//
// The wrapped client's Close method is called before each reconnect
// attempt to ensure clean teardown.
type ReconnectClient struct {
	inner     Client
	connector func(ctx context.Context) (Client, error)

	mu         sync.RWMutex
	state      ConnState
	closed     bool
	stateHooks []func(ConnState)

	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// ReconnectOption is a functional option for ReconnectClient.
type ReconnectOption func(*ReconnectClient)

// WithMaxRetries sets the maximum number of reconnection attempts.
// Zero or negative means unlimited retries.
func WithMaxRetries(n int) ReconnectOption {
	return func(c *ReconnectClient) {
		c.maxRetries = n
	}
}

// WithBaseDelay sets the initial backoff delay (default: 100ms).
func WithBaseDelay(d time.Duration) ReconnectOption {
	return func(c *ReconnectClient) {
		c.baseDelay = d
	}
}

// WithMaxDelay sets the maximum backoff delay cap (default: 30s).
func WithMaxDelay(d time.Duration) ReconnectOption {
	return func(c *ReconnectClient) {
		c.maxDelay = d
	}
}

// WithStateHook registers a callback that fires on each connection
// state transition.
func WithStateHook(hook func(ConnState)) ReconnectOption {
	return func(c *ReconnectClient) {
		c.stateHooks = append(c.stateHooks, hook)
	}
}

// NewReconnectClient wraps a Client with reconnection support.
//
// The connector function must return a fresh connected Client. It is
// called initially and on each reconnect attempt. When connector is nil,
// ReconnectClient assumes the inner client is already connected and
// simply re-calls Start on reconnect.
func NewReconnectClient(inner Client, connector func(ctx context.Context) (Client, error), opts ...ReconnectOption) *ReconnectClient {
	c := &ReconnectClient{
		inner:     inner,
		connector: connector,
		state:     StateConnected,
		maxRetries: -1, // unlimited
		baseDelay:  100 * time.Millisecond,
		maxDelay:   30 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// State returns the current connection state.
func (c *ReconnectClient) State() ConnState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// setState transitions to a new state and fires hooks.
func (c *ReconnectClient) setState(s ConnState) {
	c.mu.Lock()
	c.state = s
	hooks := make([]func(ConnState), len(c.stateHooks))
	copy(hooks, c.stateHooks)
	c.mu.Unlock()

	for _, h := range hooks {
		h(s)
	}
}

// isClosed returns true if the client has been permanently closed.
func (c *ReconnectClient) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// ListTools returns the tools exposed by the MCP server, with
// automatic reconnect on retriable errors.
func (c *ReconnectClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
	for attempt := 0; ; attempt++ {
		if c.isClosed() {
			return nil, ErrShutdown
		}

		tools, err := c.inner.ListTools(ctx)
		if err == nil {
			return tools, nil
		}

		if !IsRetriable(err) || c.maxRetries >= 0 && attempt >= c.maxRetries {
			return nil, err
		}

		if err := c.reconnect(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

// CallTool invokes a tool by name, with automatic reconnect on
// retriable errors.
func (c *ReconnectClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error) {
	for attempt := 0; ; attempt++ {
		if c.isClosed() {
			return nil, ErrShutdown
		}

		result, err := c.inner.CallTool(ctx, name, args)
		if err == nil {
			return result, nil
		}

		if !IsRetriable(err) || c.maxRetries >= 0 && attempt >= c.maxRetries {
			return nil, err
		}

		if err := c.reconnect(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

// reconnect performs one reconnection cycle with backoff.
func (c *ReconnectClient) reconnect(ctx context.Context, attempt int) error {
	c.setState(StateReconnecting)

	// Backoff: baseDelay * 2^attempt, capped at maxDelay.
	delay := time.Duration(math.Min(
		float64(c.baseDelay)*math.Pow(2, float64(attempt)),
		float64(c.maxDelay),
	))

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
		c.setState(StateDisconnected)
		return ctx.Err()
	}

	// Close the old client.
	_ = c.inner.Close()

	if c.connector != nil {
		newClient, err := c.connector(ctx)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.inner = newClient
		c.mu.Unlock()
	}

	c.setState(StateConnected)
	return nil
}

// Close permanently closes the client.
func (c *ReconnectClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	c.setState(StateDisconnected)
	return c.inner.Close()
}

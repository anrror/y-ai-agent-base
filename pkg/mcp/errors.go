package mcp

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
)

// MCP error sentinels.
var (
	// ErrConnectionClosed is returned when the underlying transport
	// connection has been closed or lost.
	ErrConnectionClosed = errors.New("mcp: connection closed")

	// ErrTimeout is returned when an operation exceeds its deadline.
	ErrTimeout = errors.New("mcp: timeout")

	// ErrServerError is returned when the MCP server returns a
	// JSON-RPC error response (non-zero error code).
	ErrServerError = errors.New("mcp: server error")

	// ErrMalformedResponse is returned when the server response
	// cannot be parsed as a valid JSON-RPC message.
	ErrMalformedResponse = errors.New("mcp: malformed response")

	// ErrNotInitialized is returned when an operation is attempted
	// before the MCP initialize handshake completes.
	ErrNotInitialized = errors.New("mcp: not initialized")

	// ErrShutdown is returned when an operation is attempted on a
	// client that is shutting down or already closed.
	ErrShutdown = errors.New("mcp: client is shutting down")
)

// IsRetriable returns true if the error suggests the operation can be
// retried on a fresh connection. This is useful for reconnection logic.
//
// Retriable errors include:
//   - Temporary network errors (DNS, TCP reset, timeout)
//   - Connection refused / closed
//   - os.ErrDeadlineExceeded
//   - context.DeadlineExceeded (not context.Canceled)
//   - ErrConnectionClosed
func IsRetriable(err error) bool {
	if err == nil {
		return false
	}

	// Our own sentinel.
	if errors.Is(err, ErrConnectionClosed) {
		return true
	}

	// Network-level temporary errors.
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		// neterrors wrapping "connection refused", "reset by peer", etc.
		return true
	}

	// OS-level errors.
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	// Syscall-level connection errors (ECONNREFUSED, ECONNRESET, etc.).
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}

	// Check for common connection error strings (Windows compat).
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connectex") || // Windows winsock
		strings.Contains(msg, "No connection could be made") {
		return true
	}

	return false
}

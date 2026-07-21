// Package session defines the session management interface and implementations.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Sentinel errors for session operations.
var (
	ErrNotFound = errors.New("session not found")
	ErrClosed   = errors.New("store closed")
	ErrExpired  = errors.New("session expired")
)

// Session represents a single user session with conversation history.
type Session struct {
	ID        string             `json:"id"`
	UserID    string             `json:"user_id"`
	AgentID   string             `json:"agent_id"`
	State     types.SessionState `json:"state"`
	Messages  []types.Message    `json:"messages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Store is the interface for session backends.
type Store interface {
	// Get retrieves a session by ID. Returns ErrNotFound if absent.
	Get(ctx context.Context, id string) (*Session, error)
	// Set upserts a session (creates if new, updates if exists).
	Set(ctx context.Context, session *Session) error
	// Delete removes a session by ID. Idempotent.
	Delete(ctx context.Context, id string) error
	// Close releases backend resources.
	Close() error
}

// NewID generates a new unique session ID using crypto/rand.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based ID if crypto/rand fails.
		return fmt.Sprintf("ses_%d", time.Now().UnixNano())
	}
	return "ses_" + hex.EncodeToString(b)
}

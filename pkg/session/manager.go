package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// DefaultMaxHistory is the default maximum number of messages to retain.
const DefaultMaxHistory = 100

// SessionManager wraps a Store and provides higher-level session operations.
type SessionManager struct {
	store      Store
	maxHistory int
	mu         sync.Mutex // serializes Update/Close read-modify-write cycles
}

// ManagerOption configures the SessionManager.
type ManagerOption func(*SessionManager)

// WithMaxHistory sets the maximum number of messages retained per session.
func WithMaxHistory(n int) ManagerOption {
	return func(m *SessionManager) {
		m.maxHistory = n
	}
}

// NewSessionManager creates a session manager backed by the given Store.
func NewSessionManager(store Store, opts ...ManagerOption) *SessionManager {
	m := &SessionManager{
		store:      store,
		maxHistory: DefaultMaxHistory,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// GetOrCreate returns an existing session or creates a new one for the user/agent pair.
func (m *SessionManager) GetOrCreate(ctx context.Context, userID, agentID, sessionID string) (*Session, error) {
	sess, err := m.store.Get(ctx, sessionID)
	if err == nil {
		return sess, nil
	}

	// Only create a new session on "not found". Propagate transient errors
	// (network timeout, connection refused, etc.) so callers can retry.
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("session get: %w", err)
	}

	now := time.Now()
	sess = &Session{
		ID:      sessionID,
		UserID:  userID,
		AgentID: agentID,
		State: types.SessionState{
			ID:        sessionID,
			UserID:    userID,
			AgentID:   agentID,
			CreatedAt: now,
			UpdatedAt: now,
			Active:    true,
		},
		Messages:  make([]types.Message, 0),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.store.Set(ctx, sess); err != nil {
		return nil, fmt.Errorf("session set: %w", err)
	}
	return sess, nil
}

// Update replaces the session's messages and trims history to maxHistory.
func (m *SessionManager) Update(ctx context.Context, id string, messages []types.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("session get: %w", err)
	}

	if messages == nil {
		messages = make([]types.Message, 0)
	}
	sess.Messages = trimHistory(messages, m.maxHistory)
	sess.UpdatedAt = time.Now()

	if err := m.store.Set(ctx, sess); err != nil {
		return fmt.Errorf("session set: %w", err)
	}
	return nil
}

// Get retrieves a session by ID.
func (m *SessionManager) Get(ctx context.Context, id string) (*Session, error) {
	sess, err := m.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}
	return sess, nil
}

// Close deactivates a session.
func (m *SessionManager) Close(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("session get: %w", err)
	}

	sess.State.Active = false
	sess.UpdatedAt = time.Now()

	if err := m.store.Set(ctx, sess); err != nil {
		return fmt.Errorf("session set: %w", err)
	}
	return nil
}

// Delete removes a session permanently.
func (m *SessionManager) Delete(ctx context.Context, id string) error {
	if err := m.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("session delete: %w", err)
	}
	return nil
}

// Store returns the underlying Store.
func (m *SessionManager) Store() Store {
	return m.store
}

// trimHistory keeps only the most recent n messages.
func trimHistory(messages []types.Message, n int) []types.Message {
	if n <= 0 {
		return nil
	}
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}

package session

import (
	"context"
	"sync"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
)

// entry wraps a stored Session with its expiration time.
type entry struct {
	session  *Session
	expireAt time.Time
}

func (e *entry) expired(now time.Time) bool {
	return e.expireAt.Before(now)
}

// MemoryStore is an in-memory session store backed by sync.Map,
// with a background goroutine for TTL-based cleanup.
type MemoryStore struct {
	data     sync.Map
	ticker   clock.Ticker
	done     chan struct{}
	reapDone chan struct{} // signaled after each reap pass (for deterministic tests)
	closed   bool
	mu       sync.Mutex
	ttl      time.Duration
	cleanup  time.Duration
	clock    clock.Clock
}

// getClock returns the configured clock, defaulting to RealClock if none set.
func (s *MemoryStore) getClock() clock.Clock {
	if s.clock == nil {
		return clock.RealClock{}
	}
	return s.clock
}

// MemoryStoreOption configures the MemoryStore.
type MemoryStoreOption func(*MemoryStore)

// WithTTL sets the default TTL for stored sessions.
func WithTTL(d time.Duration) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.ttl = d
	}
}

// WithCleanupInterval sets how often expired entries are purged.
func WithCleanupInterval(d time.Duration) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.cleanup = d
	}
}

// WithClock sets the clock implementation (useful for testing with FakeClock).
func WithClock(c clock.Clock) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.clock = c
	}
}

// NewMemoryStore creates an in-memory session store with TTL cleanup.
// Defaults: TTL=30m, cleanup interval=5m.
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	s := &MemoryStore{
		done:     make(chan struct{}),
		reapDone: make(chan struct{}, 1),
		ttl:      30 * time.Minute,
		cleanup:  5 * time.Minute,
	}
	for _, o := range opts {
		o(s)
	}
	s.ticker = s.getClock().NewTicker(s.cleanup)
	go s.reapLoop()
	return s
}

// Get retrieves a session. Returns ErrNotFound if absent or expired.
func (s *MemoryStore) Get(_ context.Context, id string) (*Session, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.mu.Unlock()

	v, ok := s.data.Load(id)
	if !ok {
		return nil, ErrNotFound
	}
	e, ok := v.(*entry)
	if !ok {
		return nil, ErrNotFound
	}
	if e.expired(s.getClock().Now()) {
		s.data.Delete(id)
		return nil, ErrNotFound
	}
	return e.session, nil
}

// Set upserts a session. If the session has no CreatedAt, it sets it to now.
func (s *MemoryStore) Set(_ context.Context, session *Session) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	now := s.getClock().Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now

	e := &entry{
		session:  session,
		expireAt: now.Add(s.ttl),
	}
	s.data.Store(session.ID, e)
	return nil
}

// Delete removes a session. Idempotent.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	s.data.Delete(id)
	return nil
}

// Len returns the current number of entries (for testing).
func (s *MemoryStore) Len() int {
	count := 0
	s.data.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Close stops the cleanup goroutine and clears all data.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.closed = true
	close(s.done)
	s.ticker.Stop()

	s.data.Range(func(k, _ any) bool {
		s.data.Delete(k)
		return true
	})
	return nil
}

// reapLoop periodically purges expired entries.
func (s *MemoryStore) reapLoop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.ticker.C():
			now := s.getClock().Now()
			s.data.Range(func(k, v any) bool {
				e, ok := v.(*entry)
				if !ok {
					s.data.Delete(k)
					return true
				}
				if e.expired(now) {
					s.data.Delete(k)
				}
				return true
			})
			// Notify tests that a reap pass has completed.
			select {
			case s.reapDone <- struct{}{}:
			default:
			}
		}
	}
}

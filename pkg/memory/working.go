package memory

import (
	"sync"
	"time"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
)

// workingEntry is a single item tracked by the working memory.
type workingEntry struct {
	userID    string
	agentID   string
	content   string
	timestamp time.Time
}

// WorkingMemory is an in-process, size-bounded sliding window. It retains the
// most recent messages per (userID, agentID) pair and evicts entries that
// exceed the configured maximum count or TTL.
type WorkingMemory struct {
	mu      sync.RWMutex
	entries []workingEntry
	maxSize int
	ttl     time.Duration
	clock   clock.Clock
}

// getClock returns the configured clock, defaulting to RealClock if none set.
func (w *WorkingMemory) getClock() clock.Clock {
	if w.clock == nil {
		return clock.RealClock{}
	}
	return w.clock
}

// NewWorkingMemory returns a WorkingMemory configured with the given maximum
// entry count and per-entry time-to-live. A zero or negative maxSize means
// unbounded; a zero or negative ttl means no time-based eviction.
func NewWorkingMemory(maxSize int, ttl time.Duration) *WorkingMemory {
	return &WorkingMemory{
		entries: make([]workingEntry, 0, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Add appends a message for the given user/agent pair. If the working memory
// is at capacity, the oldest entry is evicted.
func (w *WorkingMemory) Add(userID, agentID, content string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.evictExpiredLocked()

	entry := workingEntry{
		userID:    userID,
		agentID:   agentID,
		content:   content,
		timestamp: w.getClock().Now(),
	}
	w.entries = append(w.entries, entry)

	if w.maxSize > 0 && len(w.entries) > w.maxSize {
		// Evict oldest (FIFO sliding window).
		w.entries = w.entries[len(w.entries)-w.maxSize:]
	}
}

// GetRecent returns the most recent entries for the given userID/agentID pair,
// up to limit. A zero or negative limit returns all matching entries. TTL
// expiration is evaluated on read.
func (w *WorkingMemory) GetRecent(userID, agentID string, limit int) []string {
	// Fast path: read without blocking writers, but must skip expired entries.
	w.mu.RLock()
	if w.ttl <= 0 {
		// No TTL — just read under RLock.
		result := w.readFilteredLocked(userID, agentID, limit)
		w.mu.RUnlock()
		return result
	}

	// TTL is set — check whether any entry might be expired.
	cutoff := w.getClock().Now().Add(-w.ttl)
	hasExpired := false
	for _, entry := range w.entries {
		if !entry.timestamp.After(cutoff) {
			hasExpired = true
			break
		}
	}
	if !hasExpired {
		// All entries are still fresh — read under RLock.
		result := w.readFilteredLocked(userID, agentID, limit)
		w.mu.RUnlock()
		return result
	}
	w.mu.RUnlock()

	// Expired entries exist — upgrade to write lock, evict, then read.
	w.mu.Lock()
	w.evictExpiredLocked()
	result := w.readFilteredLocked(userID, agentID, limit)
	w.mu.Unlock()
	return result
}

// readFilteredLocked returns matching entries in chronological order.
// Caller must hold at least a read lock.
func (w *WorkingMemory) readFilteredLocked(userID, agentID string, limit int) []string {
	var result []string
	// Iterate in reverse (newest first).
	for i := len(w.entries) - 1; i >= 0; i-- {
		entry := w.entries[i]
		if entry.userID == userID && entry.agentID == agentID {
			result = append(result, entry.content)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	// Reverse to return chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// Len returns the current number of entries.
func (w *WorkingMemory) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.entries)
}

// evictExpiredLocked removes entries older than the TTL. Caller must hold
// w.mu (write lock).
func (w *WorkingMemory) evictExpiredLocked() {
	if w.ttl <= 0 {
		return
	}
	cutoff := w.getClock().Now().Add(-w.ttl)
	kept := w.entries[:0]
	for _, entry := range w.entries {
		if entry.timestamp.After(cutoff) {
			kept = append(kept, entry)
		}
	}
	w.entries = kept
}

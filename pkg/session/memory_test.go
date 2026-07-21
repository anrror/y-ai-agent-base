package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/clock"
	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func TestMemoryStore_SetGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	sess := &Session{
		ID:      "ses_test1",
		UserID:  "user1",
		AgentID: "agent1",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}

	err := store.Set(ctx, sess)
	require.NoError(t, err)

	got, err := store.Get(ctx, "ses_test1")
	require.NoError(t, err)

	assert.Equal(t, "ses_test1", got.ID)
	assert.Equal(t, "user1", got.UserID)
	assert.Equal(t, "agent1", got.AgentID)
	assert.Len(t, got.Messages, 1)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryStore_SetUpdates(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFakeClock(time.Now())
	store := NewMemoryStore(WithClock(fakeClock))
	defer func() { _ = store.Close() }()

	sess := &Session{
		ID:      "ses_test2",
		UserID:  "user1",
		AgentID: "agent1",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}
	require.NoError(t, store.Set(ctx, sess))

	got, err := store.Get(ctx, "ses_test2")
	require.NoError(t, err)
	firstUpdated := got.UpdatedAt

	// Advance clock so UpdatedAt changes
	fakeClock.Advance(10 * time.Millisecond)

	sess.Messages = append(sess.Messages, types.Message{Role: "assistant", Content: "hi!"})
	require.NoError(t, store.Set(ctx, sess))

	got, err = store.Get(ctx, "ses_test2")
	require.NoError(t, err)
	assert.Len(t, got.Messages, 2)
	assert.True(t, got.UpdatedAt.After(firstUpdated))
}

func TestMemoryStore_Delete(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	sess := &Session{ID: "ses_del", UserID: "user1", AgentID: "agent1"}
	require.NoError(t, store.Set(ctx, sess))

	err := store.Delete(ctx, "ses_del")
	require.NoError(t, err)

	_, err = store.Get(ctx, "ses_del")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryStore_DeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	err := store.Delete(ctx, "nonexistent")
	assert.NoError(t, err)
}

func TestMemoryStore_Close(t *testing.T) {
	store := NewMemoryStore()
	err := store.Close()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Get(ctx, "any")
	assert.ErrorIs(t, err, ErrClosed)

	err = store.Set(ctx, &Session{ID: "any"})
	assert.ErrorIs(t, err, ErrClosed)

	err = store.Close()
	assert.ErrorIs(t, err, ErrClosed)
}

func TestMemoryStore_TTLExpiration(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFakeClock(time.Now())
	store := NewMemoryStore(
		WithTTL(50*time.Millisecond),
		WithCleanupInterval(10*time.Minute), // disable auto cleanup for test
		WithClock(fakeClock),
	)
	defer func() { _ = store.Close() }()

	sess := &Session{ID: "ses_ttl", UserID: "user1", AgentID: "agent1"}
	require.NoError(t, store.Set(ctx, sess))

	// Should exist immediately
	_, err := store.Get(ctx, "ses_ttl")
	require.NoError(t, err)

	// Advance clock past TTL
	fakeClock.Advance(100 * time.Millisecond)

	// Get should trigger expiration check and return ErrNotFound
	_, err = store.Get(ctx, "ses_ttl")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryStore_TTLReapCleanup(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFakeClock(time.Now())
	store := NewMemoryStore(
		WithTTL(50*time.Millisecond),
		WithCleanupInterval(30*time.Millisecond),
		WithClock(fakeClock),
	)
	defer func() { _ = store.Close() }()

	sess := &Session{ID: "ses_reap", UserID: "user1", AgentID: "agent1"}
	require.NoError(t, store.Set(ctx, sess))

	assert.Equal(t, 1, store.Len())

	// Advance clock to trigger reaper
	fakeClock.Advance(200 * time.Millisecond)
	// Wait for reaper goroutine to complete its pass
	<-store.reapDone

	assert.Equal(t, 0, store.Len())
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	const (
		numGoroutines = 50
		numOps        = 100
	)

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // writers + readers

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				sess := &Session{
					ID:      "ses_concurrent_" + string(rune('0'+idx%10)),
					UserID:  "user1",
					AgentID: "agent1",
				}
				_ = store.Set(ctx, sess)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_, _ = store.Get(ctx, "ses_concurrent_0")
			}
		}()
	}

	wg.Wait()
	// No panics = pass
}

func TestMemoryStore_SetSetsTimestamps(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	sess := &Session{
		ID:      "ses_ts",
		UserID:  "user1",
		AgentID: "agent1",
	}
	require.NoError(t, store.Set(ctx, sess))

	got, err := store.Get(ctx, "ses_ts")
	require.NoError(t, err)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Equal(t, got.CreatedAt, got.UpdatedAt)
}

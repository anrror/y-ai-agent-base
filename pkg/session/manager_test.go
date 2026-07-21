package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

func TestSessionManager_GetOrCreate_CreatesNew(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)

	sess, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_new")
	require.NoError(t, err)

	assert.Equal(t, "ses_new", sess.ID)
	assert.Equal(t, "user1", sess.UserID)
	assert.Equal(t, "agent1", sess.AgentID)
	assert.True(t, sess.State.Active)
	assert.Empty(t, sess.Messages)
	assert.False(t, sess.CreatedAt.IsZero())
}

func TestSessionManager_GetOrCreate_ReturnsExisting(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)

	// Create first
	sess1, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_exist")
	require.NoError(t, err)

	// Push a message via Update to distinguish
	msg := types.Message{Role: "user", Content: "hello"}
	require.NoError(t, mgr.Update(ctx, "ses_exist", []types.Message{msg}))

	// GetOrCreate again
	sess2, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_exist")
	require.NoError(t, err)

	assert.Equal(t, sess1.ID, sess2.ID)
	assert.Len(t, sess2.Messages, 1)
	assert.Equal(t, "hello", sess2.Messages[0].Content)
}

func TestSessionManager_Update_TrimsHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store, WithMaxHistory(3))

	_, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_trim")
	require.NoError(t, err)

	// Add 5 messages
	msgs := []types.Message{
		{Role: "user", Content: "1"},
		{Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"},
		{Role: "assistant", Content: "4"},
		{Role: "user", Content: "5"},
	}
	require.NoError(t, mgr.Update(ctx, "ses_trim", msgs))

	sess, err := mgr.Get(ctx, "ses_trim")
	require.NoError(t, err)

	assert.Len(t, sess.Messages, 3)
	// Should keep the 3 most recent
	assert.Equal(t, "3", sess.Messages[0].Content)
	assert.Equal(t, "4", sess.Messages[1].Content)
	assert.Equal(t, "5", sess.Messages[2].Content)
}

func TestSessionManager_Update_NoTrimmingNeeded(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store, WithMaxHistory(10))

	_, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_notrim")
	require.NoError(t, err)

	msgs := []types.Message{
		{Role: "user", Content: "1"},
		{Role: "assistant", Content: "2"},
	}
	require.NoError(t, mgr.Update(ctx, "ses_notrim", msgs))

	sess, err := mgr.Get(ctx, "ses_notrim")
	require.NoError(t, err)
	assert.Len(t, sess.Messages, 2)
}

func TestSessionManager_Update_NilMessages(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)

	_, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_nil")
	require.NoError(t, err)

	require.NoError(t, mgr.Update(ctx, "ses_nil", nil))

	sess, err := mgr.Get(ctx, "ses_nil")
	require.NoError(t, err)
	assert.Empty(t, sess.Messages)
}

func TestSessionManager_Close_Deactivates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)

	sess, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_close")
	require.NoError(t, err)
	assert.True(t, sess.State.Active)

	require.NoError(t, mgr.Close(ctx, "ses_close"))

	sess, err = mgr.Get(ctx, "ses_close")
	require.NoError(t, err)
	assert.False(t, sess.State.Active)
}

func TestSessionManager_Delete(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)

	_, err := mgr.GetOrCreate(ctx, "user1", "agent1", "ses_del")
	require.NoError(t, err)

	require.NoError(t, mgr.Delete(ctx, "ses_del"))

	_, err = mgr.Get(ctx, "ses_del")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSessionManager_DefaultMaxHistory(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)
	assert.Equal(t, DefaultMaxHistory, mgr.maxHistory)
}

func TestSessionManager_Store(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	mgr := NewSessionManager(store)
	assert.Equal(t, store, mgr.Store())
}

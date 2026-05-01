package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore opens a fresh SQLite store in a temp dir. Used by all per-domain test files.
func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestChatPutGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chats := s.Bundle().Chats

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	c := store.Chat{
		JID:         "27821234567@s.whatsapp.net",
		Name:        "Alice",
		Kind:        "user",
		LastMsgAt:   now,
		UnreadCount: 3,
		Archived:    false,
	}
	require.NoError(t, chats.Put(ctx, c))

	got, err := chats.Get(ctx, c.JID)
	require.NoError(t, err)
	assert.Equal(t, c.JID, got.JID)
	assert.Equal(t, c.Name, got.Name)
	assert.Equal(t, c.Kind, got.Kind)
	assert.Equal(t, c.UnreadCount, got.UnreadCount)
	assert.False(t, got.Archived)
	assert.True(t, got.LastMsgAt.Equal(now), "last_msg_at roundtrip")
}

func TestChatGetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Bundle().Chats.Get(context.Background(), "nope@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestChatPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats
	jid := "27821234567@s.whatsapp.net"

	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "old", Kind: "user"}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "new", Kind: "user"}))

	got, err := chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func TestChatList(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats

	t1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: t1}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Name: "B", Kind: "user", LastMsgAt: t3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Name: "C", Kind: "user", LastMsgAt: t2, Archived: true}))

	// Default: archived excluded, ordered by last_msg_at DESC.
	got, err := chats.List(ctx, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[1].JID)

	// includeArchived: returns all, still ordered by last_msg_at DESC.
	got, err = chats.List(ctx, true)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "c@s.whatsapp.net", got[1].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[2].JID)
}

func TestChatSetArchived(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats
	jid := "x@s.whatsapp.net"
	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "X", Kind: "user"}))

	require.NoError(t, chats.SetArchived(ctx, jid, true))
	got, err := chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.True(t, got.Archived)

	require.NoError(t, chats.SetArchived(ctx, jid, false))
	got, err = chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.False(t, got.Archived)
}

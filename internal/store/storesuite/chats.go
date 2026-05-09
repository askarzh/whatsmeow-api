// Package storesuite contains shared test bodies that run against any
// store.Bundle implementation. Each Run* function exercises the same
// assertions whether the underlying dialect is SQLite or Postgres.
package storesuite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func RunChatPutGet(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()

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

func RunChatGetNotFound(t *testing.T, chats store.ChatStore) {
	_, err := chats.Get(context.Background(), "nope@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func RunChatPutIsUpsert(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()
	jid := "27821234567@s.whatsapp.net"

	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "old", Kind: "user"}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "new", Kind: "user"}))

	got, err := chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func RunChatList(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()

	t1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: t1}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Name: "B", Kind: "user", LastMsgAt: t3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Name: "C", Kind: "user", LastMsgAt: t2, Archived: true}))

	// includeArchived=false, no cursor, big limit → 2 non-archived ordered by last_msg_at DESC
	got, err := chats.List(ctx, time.Time{}, 100, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[1].JID)

	// includeArchived=true → all 3
	got, err = chats.List(ctx, time.Time{}, 100, true)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// cursor: before t3 → only a (archived c excluded)
	got, err = chats.List(ctx, t3, 100, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)

	// limit=1 from newest → b only
	got, err = chats.List(ctx, time.Time{}, 1, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
}

func RunChatCount(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()

	n, err := chats.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Kind: "user"}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Kind: "user"}))
	n, err = chats.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func RunChatTotalUnread(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()

	got, err := chats.TotalUnread(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, got)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Kind: "user", UnreadCount: 3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Kind: "user", UnreadCount: 0}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 7}))

	got, err = chats.TotalUnread(ctx)
	require.NoError(t, err)
	assert.Equal(t, 10, got)
}

func RunChatSetArchived(t *testing.T, chats store.ChatStore) {
	ctx := context.Background()
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

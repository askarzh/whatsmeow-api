package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedChat(t *testing.T, b store.Bundle, jid string) {
	t.Helper()
	require.NoError(t, b.Chats.Put(context.Background(), store.Chat{JID: jid, Name: jid, Kind: "user"}))
}

func TestMessagePutGet(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "27821234567@s.whatsapp.net"
	seedChat(t, b, chat)

	ts := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	m := store.Message{
		ID:        "MSG1",
		ChatJID:   chat,
		SenderJID: chat,
		Timestamp: ts,
		Kind:      "text",
		Body:      "hello world",
		RawMeta:   `{"foo":"bar"}`,
	}
	require.NoError(t, b.Messages.Put(ctx, m))

	got, err := b.Messages.Get(ctx, "MSG1")
	require.NoError(t, err)
	assert.Equal(t, m.ID, got.ID)
	assert.Equal(t, m.ChatJID, got.ChatJID)
	assert.Equal(t, m.Body, got.Body)
	assert.Equal(t, m.RawMeta, got.RawMeta)
	assert.True(t, got.Timestamp.Equal(ts))
	assert.Nil(t, got.EditedAt)
	assert.Nil(t, got.DeletedAt)
}

func TestMessageGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Messages.Get(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMessagePutRequiresExistingChat(t *testing.T) {
	// FK should reject a message whose chat_jid isn't in chats.
	err := newTestStore(t).Bundle().Messages.Put(context.Background(), store.Message{
		ID: "x", ChatJID: "ghost@s.whatsapp.net", SenderJID: "ghost@s.whatsapp.net",
		Timestamp: time.Now(), Kind: "text", Body: "hi",
	})
	assert.Error(t, err)
}

func TestMessageListByChat(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	mk := func(id string, secs int) store.Message {
		return store.Message{
			ID: id, ChatJID: chat, SenderJID: chat,
			Timestamp: time.Unix(int64(secs), 0).UTC(),
			Kind: "text", Body: id,
		}
	}
	for _, m := range []store.Message{mk("a", 100), mk("b", 200), mk("c", 300), mk("d", 400)} {
		require.NoError(t, b.Messages.Put(ctx, m))
	}

	// limit=2, no cursor → newest two.
	got, err := b.Messages.ListByChat(ctx, chat, 2, time.Time{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "d", got[0].ID)
	assert.Equal(t, "c", got[1].ID)

	// limit=10 with beforeTS = 300 → only a, b (older than 300, excluding 300 itself).
	got, err = b.Messages.ListByChat(ctx, chat, 10, time.Unix(300, 0).UTC())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[0].ID)
	assert.Equal(t, "a", got[1].ID)
}

func TestMessageSearchFTS(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	for _, m := range []store.Message{
		{ID: "1", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "the quick brown fox"},
		{ID: "2", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "lazy dog jumps"},
		{ID: "3", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "FOX hunts mice"},
	} {
		require.NoError(t, b.Messages.Put(ctx, m))
	}

	got, err := b.Messages.Search(ctx, "fox", 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids := []string{got[0].ID, got[1].ID}
	assert.Contains(t, ids, "1")
	assert.Contains(t, ids, "3")
}

func TestMessageSoftDelete(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	m := store.Message{
		ID: "x", ChatJID: chat, SenderJID: chat,
		Timestamp: time.Unix(100, 0).UTC(), Kind: "text", Body: "secret",
	}
	require.NoError(t, b.Messages.Put(ctx, m))

	when := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Messages.SoftDelete(ctx, "x", when))

	got, err := b.Messages.Get(ctx, "x")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
	assert.True(t, got.DeletedAt.Equal(when))

	// ListByChat excludes soft-deleted rows.
	list, err := b.Messages.ListByChat(ctx, chat, 10, time.Time{})
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestMessagePutIsUpsert(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	ts := time.Unix(1000, 0).UTC()
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: "M1", ChatJID: chat, SenderJID: chat, Timestamp: ts, Kind: "text", Body: "old body",
	}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: "M1", ChatJID: chat, SenderJID: chat, Timestamp: ts, Kind: "text", Body: "new body",
	}))

	got, err := b.Messages.Get(ctx, "M1")
	require.NoError(t, err)
	assert.Equal(t, "new body", got.Body)

	// Search must reflect the updated body and not return the old one.
	searchOld, err := b.Messages.Search(ctx, "old", 10)
	require.NoError(t, err)
	assert.Empty(t, searchOld, "old body should not appear in FTS after upsert")

	searchNew, err := b.Messages.Search(ctx, "new", 10)
	require.NoError(t, err)
	require.Len(t, searchNew, 1)
	assert.Equal(t, "M1", searchNew[0].ID)
}

func TestMessageSearchExcludesSoftDeleted(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: "M1", ChatJID: chat, SenderJID: chat,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "findable",
	}))

	// Confirm it's findable before deletion.
	got, err := b.Messages.Search(ctx, "findable", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// Soft-delete and re-search.
	require.NoError(t, b.Messages.SoftDelete(ctx, "M1", time.Unix(2000, 0).UTC()))
	got, err = b.Messages.Search(ctx, "findable", 10)
	require.NoError(t, err)
	assert.Empty(t, got, "soft-deleted messages must be excluded from search")
}

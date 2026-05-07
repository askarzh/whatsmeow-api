package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMessageForReactions seeds a chat + message so the reactions FK is satisfied.
func seedMessageForReactions(t *testing.T, b store.Bundle, chatJID, messageID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Chats.Put(ctx, store.Chat{JID: chatJID, Kind: "user"}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: messageID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}))
}

func TestReactionPutGetList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	b := s.Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")

	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: now,
	}))
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "bob@s.whatsapp.net", Emoji: "❤️", Timestamp: now,
	}))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Sorted by sender_jid ASC.
	assert.Equal(t, "alice@s.whatsapp.net", got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
	assert.Equal(t, "bob@s.whatsapp.net", got[1].SenderJID)
	assert.Equal(t, "❤️", got[1].Emoji)
	assert.True(t, got[0].Timestamp.Equal(now))
}

func TestReactionPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")

	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: t1,
	}))
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "❤️", Timestamp: t2,
	}))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "❤️", got[0].Emoji)
	assert.True(t, got[0].Timestamp.Equal(t2))
}

func TestReactionDelete(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	require.NoError(t, b.Reactions.Delete(ctx, "M1", "alice@s.whatsapp.net"))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Idempotent — deleting a non-existent reaction is a no-op.
	require.NoError(t, b.Reactions.Delete(ctx, "M1", "nobody@s.whatsapp.net"))
}

func TestReactionListEmpty(t *testing.T) {
	got, err := newTestStore(t).Bundle().Reactions.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReactionFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	// Hard-delete the parent message via a sibling sql.DB (FK cascade requires foreign_keys=on).
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, "M1")
	require.NoError(t, err)

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got, "reactions must cascade away when parent message is deleted")
}

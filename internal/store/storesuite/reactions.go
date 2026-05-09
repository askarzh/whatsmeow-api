package storesuite

import (
	"context"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
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

func RunReactionPutGetList(t *testing.T, b store.Bundle) {
	ctx := context.Background()
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

func RunReactionPutIsUpsert(t *testing.T, b store.Bundle) {
	ctx := context.Background()
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

func RunReactionDelete(t *testing.T, b store.Bundle) {
	ctx := context.Background()
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

func RunReactionListEmpty(t *testing.T, reactions store.ReactionStore) {
	got, err := reactions.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// RunReactionFKCascade asserts that reactions cascade-delete when their parent
// message is hard-deleted via the supplied callback. The public MessageStore
// only soft-deletes; each dialect's test passes its own raw-SQL hard delete.
func RunReactionFKCascade(t *testing.T, b store.Bundle, hardDelete func(messageID string)) {
	ctx := context.Background()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	hardDelete("M1")

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got, "reactions must cascade away when parent message is deleted")
}

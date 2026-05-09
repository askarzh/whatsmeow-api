package storesuite

import (
	"context"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMessageForReceipts seeds a chat + message so the receipts FK is satisfied.
func seedMessageForReceipts(t *testing.T, b store.Bundle, chatJID, messageID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Chats.Put(ctx, store.Chat{JID: chatJID, Kind: "user"}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: messageID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}))
}

func RunReceiptPutGetList(t *testing.T, b store.Bundle) {
	ctx := context.Background()
	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")

	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "delivered", Timestamp: now,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: now,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "bob@s.whatsapp.net", Type: "delivered", Timestamp: now,
	}))

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Sorted by reader_jid ASC, then type ASC.
	assert.Equal(t, "alice@s.whatsapp.net", got[0].ReaderJID)
	assert.Equal(t, "delivered", got[0].Type)
	assert.Equal(t, "alice@s.whatsapp.net", got[1].ReaderJID)
	assert.Equal(t, "read", got[1].Type)
	assert.Equal(t, "bob@s.whatsapp.net", got[2].ReaderJID)
}

func RunReceiptPutIsUpsert(t *testing.T, b store.Bundle) {
	ctx := context.Background()
	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")

	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t1,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t2,
	}))

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Timestamp.Equal(t2))
}

func RunReceiptListEmpty(t *testing.T, receipts store.ReceiptStore) {
	got, err := receipts.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// RunReceiptFKCascade asserts that receipts cascade-delete when their parent
// message is hard-deleted via the supplied callback.
func RunReceiptFKCascade(t *testing.T, b store.Bundle, hardDelete func(messageID string)) {
	ctx := context.Background()
	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: time.Now(),
	}))

	hardDelete("M1")

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

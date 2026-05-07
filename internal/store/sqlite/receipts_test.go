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

func seedMessageForReceipts(t *testing.T, b store.Bundle, chatJID, messageID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Chats.Put(ctx, store.Chat{JID: chatJID, Kind: "user"}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: messageID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}))
}

func TestReceiptPutGetList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	b := s.Bundle()
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

func TestReceiptPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
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

func TestReceiptListEmpty(t *testing.T) {
	got, err := newTestStore(t).Bundle().Receipts.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReceiptFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: time.Now(),
	}))

	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, "M1")
	require.NoError(t, err)

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

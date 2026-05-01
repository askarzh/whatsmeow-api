package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMessage(t *testing.T, b store.Bundle, msgID, chatJID string) {
	t.Helper()
	seedChat(t, b, chatJID)
	require.NoError(t, b.Messages.Put(context.Background(), store.Message{
		ID: msgID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image", Body: "",
	}))
}

func TestMediaPutGet(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessage(t, b, "M1", "c@s.whatsapp.net")

	mr := store.MediaRef{
		MessageID: "M1",
		MIME:      "image/jpeg",
		Size:      4242,
		SHA256:    "abcdef123",
		Path:      "/data/media/M1.jpg",
	}
	require.NoError(t, b.Media.Put(ctx, mr))

	got, err := b.Media.GetByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Equal(t, mr, got)
}

func TestMediaGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Media.GetByMessageID(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMediaCascadesOnMessageDelete(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessage(t, b, "M1", "c@s.whatsapp.net")
	require.NoError(t, b.Media.Put(ctx, store.MediaRef{
		MessageID: "M1", MIME: "image/jpeg", Size: 1, SHA256: "x", Path: "/p",
	}))

	rawDelete(t, dbPath, "M1")

	_, err = b.Media.GetByMessageID(ctx, "M1")
	assert.True(t, errors.Is(err, store.ErrNotFound), "media row should cascade away")
}

// rawDelete issues a hard DELETE through a sibling sql.DB so we can exercise
// the FK cascade (the public MessageStore only soft-deletes).
func rawDelete(t *testing.T, dbPath string, msgID string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, msgID)
	require.NoError(t, err)
}

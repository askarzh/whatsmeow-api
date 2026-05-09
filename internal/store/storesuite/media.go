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

// seedMessage seeds a chat + message so the media FK is satisfied.
func seedMessage(t *testing.T, b store.Bundle, msgID, chatJID string) {
	t.Helper()
	seedChat(t, b, chatJID)
	require.NoError(t, b.Messages.Put(context.Background(), store.Message{
		ID: msgID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image", Body: "",
	}))
}

func RunMediaPutGet(t *testing.T, b store.Bundle) {
	ctx := context.Background()
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

func RunMediaGetNotFound(t *testing.T, media store.MediaStore) {
	_, err := media.GetByMessageID(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

// RunMediaCascadesOnMessageDelete asserts that deleting a parent message via
// the supplied hardDelete callback also removes the associated media row (FK
// ON DELETE CASCADE). The public MessageStore only soft-deletes, so each
// dialect's test passes a hard-DELETE through its own raw connection.
func RunMediaCascadesOnMessageDelete(t *testing.T, b store.Bundle, hardDelete func(messageID string)) {
	ctx := context.Background()
	seedMessage(t, b, "M1", "c@s.whatsapp.net")
	require.NoError(t, b.Media.Put(ctx, store.MediaRef{
		MessageID: "M1", MIME: "image/jpeg", Size: 1, SHA256: "x", Path: "/p",
	}))

	hardDelete("M1")

	_, err := b.Media.GetByMessageID(ctx, "M1")
	assert.True(t, errors.Is(err, store.ErrNotFound), "media row should cascade away")
}

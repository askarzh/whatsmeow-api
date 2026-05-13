package mcp

import (
	"context"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

func TestWAListMessages_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		listMessagesFn: func(_ context.Context, chatJID string, before time.Time, limit int) ([]store.Message, error) {
			require.Equal(t, "1@s.whatsapp.net", chatJID)
			require.True(t, before.IsZero())
			return []store.Message{
				{ID: "msg1", ChatJID: chatJID, SenderJID: "2@s.whatsapp.net", Timestamp: now, Kind: "text", Body: "hello"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_messages",
		Arguments: map[string]any{"chat_jid": "1@s.whatsapp.net"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListMessagesOutput](t, res)
	require.Len(t, out.Messages, 1)
	require.Equal(t, "msg1", out.Messages[0].ID)
	require.Equal(t, "1@s.whatsapp.net", out.Messages[0].ChatJID)
	require.Equal(t, "2@s.whatsapp.net", out.Messages[0].SenderJID)
	require.Equal(t, "text", out.Messages[0].Kind)
	require.Equal(t, "hello", out.Messages[0].Body)
	require.Nil(t, out.Messages[0].ReplyTo)
	require.Nil(t, out.Messages[0].EditedAt)
	require.Nil(t, out.Messages[0].DeletedAt)
}

func TestWAListMessages_WithEditedAndDeleted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	edited := now.Add(-time.Minute)
	deleted := now.Add(-30 * time.Second)
	replyTo := "original-msg"
	svc := &fakeService{
		listMessagesFn: func(_ context.Context, chatJID string, before time.Time, limit int) ([]store.Message, error) {
			return []store.Message{
				{
					ID:        "msg2",
					ChatJID:   chatJID,
					SenderJID: "2@s.whatsapp.net",
					Timestamp: now,
					Kind:      "text",
					Body:      "edited",
					ReplyTo:   replyTo,
					EditedAt:  &edited,
					DeletedAt: &deleted,
				},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_messages",
		Arguments: map[string]any{"chat_jid": "1@s.whatsapp.net"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListMessagesOutput](t, res)
	require.Len(t, out.Messages, 1)
	require.NotNil(t, out.Messages[0].ReplyTo)
	require.Equal(t, replyTo, *out.Messages[0].ReplyTo)
	require.NotNil(t, out.Messages[0].EditedAt)
	require.NotNil(t, out.Messages[0].DeletedAt)
}

func TestWAListMessages_InvalidBefore(t *testing.T) {
	svc := &fakeService{}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_messages",
		Arguments: map[string]any{"chat_jid": "1@s.whatsapp.net", "before": "bad-time"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWASearchMessages_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		searchMessagesFn: func(_ context.Context, query string, limit int) ([]store.Message, error) {
			require.Equal(t, "hello", query)
			require.Equal(t, 20, limit)
			return []store.Message{
				{ID: "msg1", ChatJID: "1@s.whatsapp.net", SenderJID: "2@s.whatsapp.net", Timestamp: now, Kind: "text", Body: "hello world"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_search_messages",
		Arguments: map[string]any{"query": "hello", "limit": 20},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListMessagesOutput](t, res)
	require.Len(t, out.Messages, 1)
	require.Equal(t, "hello world", out.Messages[0].Body)
}

func TestWAListReactions_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		listReactionsFn: func(_ context.Context, messageID string) ([]store.Reaction, error) {
			require.Equal(t, "msg1", messageID)
			return []store.Reaction{
				{MessageID: "msg1", SenderJID: "2@s.whatsapp.net", Emoji: "👍", Timestamp: now},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_reactions",
		Arguments: map[string]any{"message_id": "msg1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListReactionsOutput](t, res)
	require.Len(t, out.Reactions, 1)
	require.Equal(t, "msg1", out.Reactions[0].MessageID)
	require.Equal(t, "2@s.whatsapp.net", out.Reactions[0].SenderJID)
	require.Equal(t, "👍", out.Reactions[0].Emoji)
}

func TestWAListReceipts_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		listReceiptsFn: func(_ context.Context, messageID string) ([]store.Receipt, error) {
			require.Equal(t, "msg1", messageID)
			return []store.Receipt{
				{MessageID: "msg1", ReaderJID: "2@s.whatsapp.net", Type: "read", Timestamp: now},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_receipts",
		Arguments: map[string]any{"message_id": "msg1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListReceiptsOutput](t, res)
	require.Len(t, out.Receipts, 1)
	require.Equal(t, "msg1", out.Receipts[0].MessageID)
	require.Equal(t, "2@s.whatsapp.net", out.Receipts[0].ReaderJID)
	require.Equal(t, "read", out.Receipts[0].Type)
}

func TestWAGetMedia_HappyPath(t *testing.T) {
	svc := &fakeService{
		getMediaRefFn: func(_ context.Context, messageID string) (store.MediaRef, error) {
			require.Equal(t, "msg1", messageID)
			return store.MediaRef{
				MessageID: "msg1",
				MIME:      "image/jpeg",
				Size:      1024,
				SHA256:    "abc123",
				Path:      "/data/media/abc123.jpg",
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_get_media",
		Arguments: map[string]any{"message_id": "msg1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waMediaRefOutput](t, res)
	require.Equal(t, "msg1", out.MessageID)
	require.Equal(t, "image/jpeg", out.MIME)
	require.Equal(t, int64(1024), out.Size)
	require.Equal(t, "abc123", out.SHA256)
	require.Equal(t, "/data/media/abc123.jpg", out.Path)
}

func TestWAGetMedia_NotFound(t *testing.T) {
	svc := &fakeService{
		getMediaRefFn: func(_ context.Context, messageID string) (store.MediaRef, error) {
			return store.MediaRef{}, store.ErrNotFound
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_get_media",
		Arguments: map[string]any{"message_id": "nonexistent"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

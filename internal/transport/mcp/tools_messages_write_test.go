package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

// --- wa_send_text ---

func TestWASendText_HappyPath(t *testing.T) {
	svc := &fakeService{
		sendTextFn: func(_ context.Context, jid, text, reply string) (store.Message, error) {
			require.Equal(t, "x@s.whatsapp.net", jid)
			require.Equal(t, "hi", text)
			require.Empty(t, reply)
			return store.Message{ID: "msg-out", Body: "hi"}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_send_text",
		Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net", "text": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waSendTextOutput](t, res)
	require.Equal(t, "msg-out", out.Message.ID)
	require.Equal(t, "hi", out.Message.Body)
}

func TestWASendText_WithReplyTo(t *testing.T) {
	svc := &fakeService{
		sendTextFn: func(_ context.Context, jid, text, reply string) (store.Message, error) {
			require.Equal(t, "parent-msg-id", reply)
			return store.Message{ID: "reply-msg", Body: text, ReplyTo: reply}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_send_text",
		Arguments: map[string]any{
			"chat_jid": "x@s.whatsapp.net",
			"text":     "hello reply",
			"reply_to": "parent-msg-id",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waSendTextOutput](t, res)
	require.Equal(t, "reply-msg", out.Message.ID)
	require.NotNil(t, out.Message.ReplyTo)
	require.Equal(t, "parent-msg-id", *out.Message.ReplyTo)
}

func TestWASendText_InvalidRequest(t *testing.T) {
	svc := &fakeService{
		sendTextFn: func(_ context.Context, _, _, _ string) (store.Message, error) {
			return store.Message{}, fmt.Errorf("%w: text is required", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_send_text",
		Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net", "text": ""},
	})
	require.True(t, res.IsError)
}

// --- wa_send_media ---

func TestWASendMedia_HappyPath(t *testing.T) {
	body := []byte("image-bytes")
	svc := &fakeService{
		sendMediaFn: func(_ context.Context, req service.SendMediaRequest) (store.Message, error) {
			require.Equal(t, body, req.Body)
			require.Equal(t, "image", req.Kind)
			require.Equal(t, "x@s.whatsapp.net", req.ChatJID)
			return store.Message{ID: "media-msg", Kind: "image"}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_send_media",
		Arguments: map[string]any{
			"chat_jid":    "x@s.whatsapp.net",
			"kind":        "image",
			"body_base64": base64.StdEncoding.EncodeToString(body),
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waSendMediaOutput](t, res)
	require.Equal(t, "media-msg", out.Message.ID)
}

func TestWASendMedia_BadBase64(t *testing.T) {
	svc := &fakeService{
		sendMediaFn: func(_ context.Context, _ service.SendMediaRequest) (store.Message, error) {
			t.Fatal("service should not be called when base64 decode fails")
			return store.Message{}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_send_media",
		Arguments: map[string]any{
			"chat_jid":    "x@s.whatsapp.net",
			"kind":        "image",
			"body_base64": "!!!not base64!!!",
		},
	})
	require.True(t, res.IsError)
}

// --- wa_edit_message ---

func TestWAEditMessage_HappyPath(t *testing.T) {
	svc := &fakeService{
		editMessageFn: func(_ context.Context, msgID, text string) (store.Message, error) {
			require.Equal(t, "msg-123", msgID)
			require.Equal(t, "corrected text", text)
			return store.Message{ID: msgID, Body: text}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_edit_message",
		Arguments: map[string]any{
			"message_id": "msg-123",
			"text":       "corrected text",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waEditMessageOutput](t, res)
	require.Equal(t, "msg-123", out.Message.ID)
	require.Equal(t, "corrected text", out.Message.Body)
}

func TestWAEditMessage_ForbiddenMapsToToolError(t *testing.T) {
	svc := &fakeService{
		editMessageFn: func(_ context.Context, _, _ string) (store.Message, error) {
			return store.Message{}, fmt.Errorf("%w: not your message", service.ErrForbidden)
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_edit_message",
		Arguments: map[string]any{
			"message_id": "msg-abc",
			"text":       "new text",
		},
	})
	require.True(t, res.IsError)
}

// --- wa_delete_message ---

func TestWADeleteMessage_HappyPath(t *testing.T) {
	svc := &fakeService{
		deleteMessageFn: func(_ context.Context, msgID string) error {
			require.Equal(t, "del-msg-id", msgID)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_delete_message",
		Arguments: map[string]any{"message_id": "del-msg-id"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

func TestWADeleteMessage_ForbiddenMapsToToolError(t *testing.T) {
	svc := &fakeService{
		deleteMessageFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("%w: cannot delete others' messages", service.ErrForbidden)
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_delete_message",
		Arguments: map[string]any{"message_id": "msg-xyz"},
	})
	require.True(t, res.IsError)
}

// --- wa_react ---

func TestWAReact_HappyPath(t *testing.T) {
	svc := &fakeService{
		sendReactionFn: func(_ context.Context, msgID, emoji string) error {
			require.Equal(t, "msg-react", msgID)
			require.Equal(t, "👍", emoji)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_react",
		Arguments: map[string]any{"message_id": "msg-react", "emoji": "👍"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

func TestWAReact_ClearReaction(t *testing.T) {
	called := false
	svc := &fakeService{
		sendReactionFn: func(_ context.Context, msgID, emoji string) error {
			called = true
			require.Equal(t, "", emoji)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_react",
		Arguments: map[string]any{"message_id": "msg-react", "emoji": ""},
	})
	require.False(t, res.IsError)
	require.True(t, called)
}

// --- wa_mark_read ---

func TestWAMarkRead_HappyPath(t *testing.T) {
	svc := &fakeService{
		markReadFn: func(_ context.Context, msgID string) error {
			require.Equal(t, "msg-read-id", msgID)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_mark_read",
		Arguments: map[string]any{"message_id": "msg-read-id"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

// --- wa_typing ---

func TestWATyping_HappyPath(t *testing.T) {
	svc := &fakeService{
		sendTypingFn: func(_ context.Context, jid, state string) error {
			require.Equal(t, "chat@s.whatsapp.net", jid)
			require.Equal(t, "composing", state)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_typing",
		Arguments: map[string]any{
			"chat_jid": "chat@s.whatsapp.net",
			"state":    "composing",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

func TestWATyping_Paused(t *testing.T) {
	svc := &fakeService{
		sendTypingFn: func(_ context.Context, _, state string) error {
			require.Equal(t, "paused", state)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, _ := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_typing",
		Arguments: map[string]any{
			"chat_jid": "chat@s.whatsapp.net",
			"state":    "paused",
		},
	})
	require.False(t, res.IsError)
}

package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

func TestWAListChats_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		listChatsFn: func(_ context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
			require.True(t, before.IsZero())
			require.Equal(t, 0, limit)
			require.False(t, includeArchived)
			return []store.Chat{
				{JID: "1@s.whatsapp.net", Name: "Alice", Kind: "user", LastMsgAt: now, UnreadCount: 2, Archived: false},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_list_chats"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListChatsOutput](t, res)
	require.Len(t, out.Chats, 1)
	require.Equal(t, "1@s.whatsapp.net", out.Chats[0].JID)
	require.NotNil(t, out.Chats[0].Name)
	require.Equal(t, "Alice", *out.Chats[0].Name)
	require.Equal(t, "user", out.Chats[0].Kind)
	require.Equal(t, 2, out.Chats[0].UnreadCount)
	require.NotNil(t, out.Chats[0].LastMsgAt)
}

func TestWAListChats_WithBeforeParam(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	svc := &fakeService{
		listChatsFn: func(_ context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
			require.Equal(t, ts, before)
			require.Equal(t, 5, limit)
			require.True(t, includeArchived)
			return []store.Chat{}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_chats",
		Arguments: map[string]any{"before": "2025-01-15T10:00:00Z", "limit": 5, "include_archived": true},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListChatsOutput](t, res)
	require.Empty(t, out.Chats)
}

func TestWAListChats_InvalidBefore(t *testing.T) {
	svc := &fakeService{}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_chats",
		Arguments: map[string]any{"before": "not-a-timestamp"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWAGetChat_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeService{
		getChatFn: func(_ context.Context, jid string) (store.Chat, error) {
			require.Equal(t, "1@s.whatsapp.net", jid)
			return store.Chat{JID: jid, Name: "Alice", Kind: "user", LastMsgAt: now, UnreadCount: 1, Archived: false}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_get_chat",
		Arguments: map[string]any{"jid": "1@s.whatsapp.net"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waChatOutput](t, res)
	require.Equal(t, "1@s.whatsapp.net", out.JID)
	require.Equal(t, "user", out.Kind)
	require.Equal(t, 1, out.UnreadCount)
}

func TestWAGetChat_NotFound(t *testing.T) {
	svc := &fakeService{
		getChatFn: func(_ context.Context, jid string) (store.Chat, error) {
			return store.Chat{}, store.ErrNotFound
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_get_chat",
		Arguments: map[string]any{"jid": "unknown@s.whatsapp.net"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWAGetChat_ServiceError(t *testing.T) {
	svc := &fakeService{
		getChatFn: func(_ context.Context, jid string) (store.Chat, error) {
			return store.Chat{}, errors.New("db gone")
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_get_chat",
		Arguments: map[string]any{"jid": "1@s.whatsapp.net"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

package mcp

import (
	"context"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

// parseRFC3339OrZero parses an RFC3339 timestamp string. The empty string
// returns a zero time.Time (which callers treat as "no bound"). Any other
// invalid string returns a tool-error result so the handler can early-return.
func parseRFC3339OrZero(s string) (time.Time, *mcpsdk.CallToolResult) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{&mcpsdk.TextContent{
				Text: fmt.Sprintf("invalid before timestamp %q: must be RFC3339", s),
			}},
		}
	}
	return t, nil
}

// --- output types (mirror REST JSON shapes exactly) ---

type waChatOutput struct {
	JID         string  `json:"jid"`
	Name        *string `json:"name,omitempty"`
	Kind        string  `json:"kind"`
	LastMsgAt   *string `json:"last_msg_at,omitempty"`
	UnreadCount int     `json:"unread_count"`
	Archived    bool    `json:"archived"`
}

type waListChatsOutput struct {
	Chats []waChatOutput `json:"chats"`
}

// --- input types ---

type waListChatsInput struct {
	Before          string `json:"before,omitempty" jsonschema:"RFC3339 timestamp; return only chats with last message before this time"`
	Limit           int    `json:"limit,omitempty" jsonschema:"Maximum number of chats to return; 0 means service default"`
	IncludeArchived bool   `json:"include_archived,omitempty" jsonschema:"Whether to include archived chats"`
}

type waGetChatInput struct {
	JID string `json:"jid" jsonschema:"WhatsApp JID of the chat"`
}

// --- conversion helpers ---

func chatToOutput(c store.Chat) waChatOutput {
	out := waChatOutput{
		JID:         c.JID,
		Kind:        c.Kind,
		UnreadCount: c.UnreadCount,
		Archived:    c.Archived,
	}
	if c.Name != "" {
		s := c.Name
		out.Name = &s
	}
	if !c.LastMsgAt.IsZero() {
		s := c.LastMsgAt.UTC().Format(time.RFC3339)
		out.LastMsgAt = &s
	}
	return out
}

func chatsToOutput(chats []store.Chat) []waChatOutput {
	out := make([]waChatOutput, 0, len(chats))
	for _, c := range chats {
		out = append(out, chatToOutput(c))
	}
	return out
}

// --- registrar ---

func registerChatTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_chats",
		Description: "List WhatsApp chats from the local cache, ordered by most-recent message. Supports cursor-based pagination via the before timestamp. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListChatsInput) (*mcpsdk.CallToolResult, waListChatsOutput, error) {
		before, badRes := parseRFC3339OrZero(in.Before)
		if badRes != nil {
			return badRes, waListChatsOutput{}, nil
		}
		chats, err := d.Service.ListChats(ctx, before, in.Limit, in.IncludeArchived)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListChatsOutput{}, terr
		}
		return nil, waListChatsOutput{Chats: chatsToOutput(chats)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_get_chat",
		Description: "Get a single WhatsApp chat by JID. Returns not-found error if the JID is not in the local cache. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGetChatInput) (*mcpsdk.CallToolResult, waChatOutput, error) {
		c, err := d.Service.GetChat(ctx, in.JID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waChatOutput{}, terr
		}
		return nil, chatToOutput(c), nil
	})
}

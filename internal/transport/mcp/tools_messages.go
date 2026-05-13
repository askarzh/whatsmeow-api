package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

// --- output types ---

type waMessageOutput struct {
	ID        string  `json:"id"`
	ChatJID   string  `json:"chat_jid"`
	SenderJID string  `json:"sender_jid"`
	TS        string  `json:"ts"`
	Kind      string  `json:"kind"`
	Body      string  `json:"body"`
	ReplyTo   *string `json:"reply_to,omitempty"`
	EditedAt  *string `json:"edited_at,omitempty"`
	DeletedAt *string `json:"deleted_at,omitempty"`
}

type waListMessagesOutput struct {
	Messages []waMessageOutput `json:"messages"`
}

type waReactionOutput struct {
	MessageID string `json:"message_id"`
	SenderJID string `json:"sender_jid"`
	Emoji     string `json:"emoji"`
	TS        string `json:"ts"`
}

type waListReactionsOutput struct {
	Reactions []waReactionOutput `json:"reactions"`
}

type waReceiptOutput struct {
	MessageID string `json:"message_id"`
	ReaderJID string `json:"reader_jid"`
	Type      string `json:"type"`
	TS        string `json:"ts"`
}

type waListReceiptsOutput struct {
	Receipts []waReceiptOutput `json:"receipts"`
}

type waMediaRefOutput struct {
	MessageID string `json:"message_id"`
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Path      string `json:"path"`
}

// --- input types ---

type waListMessagesInput struct {
	ChatJID string `json:"chat_jid" jsonschema:"WhatsApp JID of the chat to list messages from"`
	Before  string `json:"before,omitempty" jsonschema:"RFC3339 timestamp; return only messages before this time"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum number of messages to return; 0 means service default"`
}

type waSearchMessagesInput struct {
	Query string `json:"query" jsonschema:"Full-text search query matched against message bodies"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return; 0 means service default"`
}

type waListReactionsInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message whose reactions to list"`
}

type waListReceiptsInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message whose delivery/read receipts to list"`
}

type waGetMediaInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message whose media attachment to retrieve"`
}

// --- conversion helpers ---

func messageToOutput(m store.Message) waMessageOutput {
	out := waMessageOutput{
		ID:        m.ID,
		ChatJID:   m.ChatJID,
		SenderJID: m.SenderJID,
		TS:        m.Timestamp.UTC().Format(time.RFC3339),
		Kind:      m.Kind,
		Body:      m.Body,
	}
	if m.ReplyTo != "" {
		s := m.ReplyTo
		out.ReplyTo = &s
	}
	if m.EditedAt != nil {
		s := m.EditedAt.UTC().Format(time.RFC3339)
		out.EditedAt = &s
	}
	if m.DeletedAt != nil {
		s := m.DeletedAt.UTC().Format(time.RFC3339)
		out.DeletedAt = &s
	}
	return out
}

func messagesToOutput(msgs []store.Message) []waMessageOutput {
	out := make([]waMessageOutput, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToOutput(m))
	}
	return out
}

func reactionToOutput(r store.Reaction) waReactionOutput {
	return waReactionOutput{
		MessageID: r.MessageID,
		SenderJID: r.SenderJID,
		Emoji:     r.Emoji,
		TS:        r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func reactionsToOutput(rs []store.Reaction) []waReactionOutput {
	out := make([]waReactionOutput, 0, len(rs))
	for _, r := range rs {
		out = append(out, reactionToOutput(r))
	}
	return out
}

func receiptToOutput(r store.Receipt) waReceiptOutput {
	return waReceiptOutput{
		MessageID: r.MessageID,
		ReaderJID: r.ReaderJID,
		Type:      r.Type,
		TS:        r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func receiptsToOutput(rs []store.Receipt) []waReceiptOutput {
	out := make([]waReceiptOutput, 0, len(rs))
	for _, r := range rs {
		out = append(out, receiptToOutput(r))
	}
	return out
}

func mediaRefToOutput(ref store.MediaRef) waMediaRefOutput {
	return waMediaRefOutput{
		MessageID: ref.MessageID,
		MIME:      ref.MIME,
		Size:      ref.Size,
		SHA256:    ref.SHA256,
		Path:      ref.Path,
	}
}

// --- registrar ---

func registerReadOnlyMessageTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_messages",
		Description: "List messages in a WhatsApp chat from the local cache, ordered by most-recent first. Supports cursor-based pagination via the before timestamp. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListMessagesInput) (*mcpsdk.CallToolResult, waListMessagesOutput, error) {
		before, badRes := parseRFC3339OrZero(in.Before)
		if badRes != nil {
			return badRes, waListMessagesOutput{}, nil
		}
		msgs, err := d.Service.ListMessages(ctx, in.ChatJID, before, in.Limit)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListMessagesOutput{}, terr
		}
		return nil, waListMessagesOutput{Messages: messagesToOutput(msgs)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_search_messages",
		Description: "Full-text search across all cached WhatsApp messages. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSearchMessagesInput) (*mcpsdk.CallToolResult, waListMessagesOutput, error) {
		msgs, err := d.Service.SearchMessages(ctx, in.Query, in.Limit)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListMessagesOutput{}, terr
		}
		return nil, waListMessagesOutput{Messages: messagesToOutput(msgs)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_reactions",
		Description: "List emoji reactions on a WhatsApp message. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListReactionsInput) (*mcpsdk.CallToolResult, waListReactionsOutput, error) {
		reactions, err := d.Service.ListReactions(ctx, in.MessageID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListReactionsOutput{}, terr
		}
		return nil, waListReactionsOutput{Reactions: reactionsToOutput(reactions)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_receipts",
		Description: "List delivery and read receipts for a WhatsApp message. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListReceiptsInput) (*mcpsdk.CallToolResult, waListReceiptsOutput, error) {
		receipts, err := d.Service.ListReceipts(ctx, in.MessageID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListReceiptsOutput{}, terr
		}
		return nil, waListReceiptsOutput{Receipts: receiptsToOutput(receipts)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_get_media",
		Description: "Get the media attachment reference for a WhatsApp message. Returns the on-disk path, MIME type, size, and SHA-256 of the file. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGetMediaInput) (*mcpsdk.CallToolResult, waMediaRefOutput, error) {
		ref, err := d.Service.GetMediaRef(ctx, in.MessageID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waMediaRefOutput{}, terr
		}
		return nil, mediaRefToOutput(ref), nil
	})
}

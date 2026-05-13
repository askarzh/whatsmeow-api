package mcp

import (
	"context"
	"encoding/base64"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// boolPtr returns a pointer to b. Used for ToolAnnotations fields that are *bool.
func boolPtr(b bool) *bool { return &b }

// --- input types ---

type waSendTextInput struct {
	ChatJID string `json:"chat_jid" jsonschema:"WhatsApp JID of the chat to send to (user or group)"`
	Text    string `json:"text" jsonschema:"Message body text; max 4096 bytes"`
	ReplyTo string `json:"reply_to,omitempty" jsonschema:"Optional: message ID to reply to"`
}

type waSendTextOutput struct {
	Message waMessageOutput `json:"message"`
}

type waSendMediaInput struct {
	ChatJID    string `json:"chat_jid" jsonschema:"WhatsApp JID of the chat to send to"`
	Kind       string `json:"kind" jsonschema:"Media kind: image or document"`
	BodyBase64 string `json:"body_base64" jsonschema:"Standard base64-encoded media bytes"`
	Caption    string `json:"caption,omitempty" jsonschema:"Optional caption text (image only)"`
	MIMEType   string `json:"mime_type,omitempty" jsonschema:"MIME type; sniffed from bytes if omitted"`
	Filename   string `json:"filename,omitempty" jsonschema:"File name; required when kind=document"`
}

type waSendMediaOutput struct {
	Message waMessageOutput `json:"message"`
}

type waEditMessageInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message to edit"`
	Text      string `json:"text" jsonschema:"Replacement text; max 4096 bytes"`
}

type waEditMessageOutput struct {
	Message waMessageOutput `json:"message"`
}

// waMessageIDInput is a single-field input reused by wa_delete_message and wa_mark_read.
type waMessageIDInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the target message"`
}

type waReactInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message to react to"`
	Emoji     string `json:"emoji" jsonschema:"Single emoji to add; empty string clears an existing reaction"`
}

type waTypingInput struct {
	ChatJID string `json:"chat_jid" jsonschema:"WhatsApp JID of the chat"`
	State   string `json:"state" jsonschema:"Presence state: composing or paused"`
}

// waOK is the universal acknowledgement output for mutation-only tools.
type waOK struct {
	OK bool `json:"ok"`
}

// registerWriteMessageTools adds the 7 write-side message tools to srv.
func registerWriteMessageTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_send_text",
		Description: "Send a text message to a chat (1:1 or group). Returns the persisted message row.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSendTextInput) (*mcpsdk.CallToolResult, waSendTextOutput, error) {
		m, err := d.Service.SendText(ctx, in.ChatJID, in.Text, in.ReplyTo)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waSendTextOutput{}, terr
		}
		return nil, waSendTextOutput{Message: messageToOutput(m)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_send_media",
		Description: "Send a media message (image or document). Body is base64-encoded; prefer small payloads — large files should be sent via the REST /v1/media multipart endpoint.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSendMediaInput) (*mcpsdk.CallToolResult, waSendMediaOutput, error) {
		body, decodeErr := base64.StdEncoding.DecodeString(in.BodyBase64)
		if decodeErr != nil {
			return toolErr("invalid request: body_base64 must be standard base64"), waSendMediaOutput{}, nil
		}
		m, err := d.Service.SendMedia(ctx, service.SendMediaRequest{
			ChatJID:  in.ChatJID,
			Kind:     in.Kind,
			Caption:  in.Caption,
			Filename: in.Filename,
			MIME:     in.MIMEType,
			Body:     body,
		})
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waSendMediaOutput{}, terr
		}
		return nil, waSendMediaOutput{Message: messageToOutput(m)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_edit_message",
		Description: "Edit a previously-sent text message you own. Returns the updated message.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waEditMessageInput) (*mcpsdk.CallToolResult, waEditMessageOutput, error) {
		m, err := d.Service.EditMessage(ctx, in.MessageID, in.Text)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waEditMessageOutput{}, terr
		}
		return nil, waEditMessageOutput{Message: messageToOutput(m)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_delete_message",
		Description: "Delete a message you own (revoke for everyone). Irreversible on the network.",
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waOK, error) {
		err := d.Service.DeleteMessage(ctx, in.MessageID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waOK{}, terr
		}
		return nil, waOK{OK: true}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_react",
		Description: "Add or replace a reaction on a message. Pass emoji=\"\" to clear an existing reaction.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waReactInput) (*mcpsdk.CallToolResult, waOK, error) {
		err := d.Service.SendReaction(ctx, in.MessageID, in.Emoji)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waOK{}, terr
		}
		return nil, waOK{OK: true}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_mark_read",
		Description: "Mark an inbound message (and prior messages in the same chat) as read on the network.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waOK, error) {
		err := d.Service.MarkMessageRead(ctx, in.MessageID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waOK{}, terr
		}
		return nil, waOK{OK: true}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_typing",
		Description: "Send a typing-presence indicator to a chat. state must be 'composing' or 'paused'.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waTypingInput) (*mcpsdk.CallToolResult, waOK, error) {
		err := d.Service.SendTyping(ctx, in.ChatJID, in.State)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waOK{}, terr
		}
		return nil, waOK{OK: true}, nil
	})
}

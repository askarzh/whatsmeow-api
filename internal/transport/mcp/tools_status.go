package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type waStatusInput struct{}

type waStatusOutput struct {
	Connected bool   `json:"connected" jsonschema:"Whether the daemon is currently connected to WhatsApp"`
	JID       string `json:"jid,omitempty" jsonschema:"Logged-in WhatsApp JID; empty until paired"`
	PushName  string `json:"push_name,omitempty" jsonschema:"Display name of the logged-in account"`
}

type waStatsOutput struct {
	Chats       int `json:"chats"`
	Messages    int `json:"messages"`
	Contacts    int `json:"contacts"`
	UnreadTotal int `json:"unread_total"`
}

func registerStatusTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_status",
		Description: "Return the daemon's current WhatsApp connection state and the logged-in JID. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ waStatusInput) (*mcpsdk.CallToolResult, waStatusOutput, error) {
		s, err := d.Service.Status(ctx)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waStatusOutput{}, terr
		}
		out := waStatusOutput{Connected: s.Connected}
		if s.JID != nil {
			out.JID = *s.JID
		}
		if s.PushName != nil {
			out.PushName = *s.PushName
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_stats",
		Description: "Return aggregate counts of the local cache (chats, messages, contacts, unread total). Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waStatsOutput, error) {
		s, err := d.Service.Stats(ctx)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waStatsOutput{}, terr
		}
		return nil, waStatsOutput{
			Chats:       s.Chats,
			Messages:    s.Messages,
			Contacts:    s.Contacts,
			UnreadTotal: s.UnreadTotal,
		}, nil
	})
}

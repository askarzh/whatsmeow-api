package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

// --- output types ---

type waContactOutput struct {
	JID          string  `json:"jid"`
	PushName     *string `json:"push_name,omitempty"`
	FullName     *string `json:"full_name,omitempty"`
	BusinessName *string `json:"business_name,omitempty"`
}

type waListContactsOutput struct {
	Contacts []waContactOutput `json:"contacts"`
}

// --- input types ---

type waSearchContactsInput struct {
	Query string `json:"query" jsonschema:"Search query matched against contact names and JIDs"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return; 0 means service default"`
}

// --- conversion helpers ---

func contactToOutput(c store.Contact) waContactOutput {
	out := waContactOutput{JID: c.JID}
	if c.PushName != "" {
		s := c.PushName
		out.PushName = &s
	}
	if c.FullName != "" {
		s := c.FullName
		out.FullName = &s
	}
	if c.BusinessName != "" {
		s := c.BusinessName
		out.BusinessName = &s
	}
	return out
}

func contactsToOutput(contacts []store.Contact) []waContactOutput {
	out := make([]waContactOutput, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, contactToOutput(c))
	}
	return out
}

// --- registrar ---

func registerContactTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_contacts",
		Description: "List all WhatsApp contacts from the local cache. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waListContactsOutput, error) {
		contacts, err := d.Service.ListContacts(ctx)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListContactsOutput{}, terr
		}
		return nil, waListContactsOutput{Contacts: contactsToOutput(contacts)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_search_contacts",
		Description: "Search WhatsApp contacts by name or JID substring. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSearchContactsInput) (*mcpsdk.CallToolResult, waListContactsOutput, error) {
		contacts, err := d.Service.SearchContacts(ctx, in.Query, in.Limit)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListContactsOutput{}, terr
		}
		return nil, waListContactsOutput{Contacts: contactsToOutput(contacts)}, nil
	})
}

package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

func TestWAListContacts_HappyPath(t *testing.T) {
	svc := &fakeService{
		listContactsFn: func(_ context.Context) ([]store.Contact, error) {
			return []store.Contact{
				{JID: "1@s.whatsapp.net", PushName: "Alice", FullName: "Alice Smith", BusinessName: ""},
				{JID: "2@s.whatsapp.net", PushName: "Bob", FullName: "", BusinessName: "Bob Corp"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_list_contacts"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListContactsOutput](t, res)
	require.Len(t, out.Contacts, 2)
	require.Equal(t, "1@s.whatsapp.net", out.Contacts[0].JID)
	require.NotNil(t, out.Contacts[0].PushName)
	require.Equal(t, "Alice", *out.Contacts[0].PushName)
	require.NotNil(t, out.Contacts[0].FullName)
	require.Equal(t, "Alice Smith", *out.Contacts[0].FullName)
	require.Nil(t, out.Contacts[0].BusinessName) // empty → omitted
	require.NotNil(t, out.Contacts[1].BusinessName)
	require.Equal(t, "Bob Corp", *out.Contacts[1].BusinessName)
}

func TestWAListContacts_Empty(t *testing.T) {
	svc := &fakeService{
		listContactsFn: func(_ context.Context) ([]store.Contact, error) {
			return []store.Contact{}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_list_contacts"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListContactsOutput](t, res)
	require.Empty(t, out.Contacts)
}

func TestWASearchContacts_HappyPath(t *testing.T) {
	svc := &fakeService{
		searchContactsFn: func(_ context.Context, query string, limit int) ([]store.Contact, error) {
			require.Equal(t, "alice", query)
			require.Equal(t, 10, limit)
			return []store.Contact{
				{JID: "1@s.whatsapp.net", PushName: "Alice"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_search_contacts",
		Arguments: map[string]any{"query": "alice", "limit": 10},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListContactsOutput](t, res)
	require.Len(t, out.Contacts, 1)
	require.Equal(t, "1@s.whatsapp.net", out.Contacts[0].JID)
}

func TestWASearchContacts_ServiceError(t *testing.T) {
	svc := &fakeService{
		searchContactsFn: func(_ context.Context, query string, limit int) ([]store.Contact, error) {
			return nil, service.ErrInvalidRequest
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_search_contacts",
		Arguments: map[string]any{"query": ""},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// fakeService stubs service.Service for tool tests; each test wires only the
// fields it exercises. A nil function field panics on access — that surfaces
// missing wiring as a test bug instead of a confusing nil-method error.
type fakeService struct {
	service.Service // unset methods panic via embedded-nil deref
	statusFn         func(context.Context) (waclient.Status, error)
	statsFn          func(context.Context) (service.Stats, error)
	listChatsFn      func(context.Context, time.Time, int, bool) ([]store.Chat, error)
	getChatFn        func(context.Context, string) (store.Chat, error)
	listMessagesFn   func(context.Context, string, time.Time, int) ([]store.Message, error)
	searchMessagesFn func(context.Context, string, int) ([]store.Message, error)
	listContactsFn   func(context.Context) ([]store.Contact, error)
	searchContactsFn func(context.Context, string, int) ([]store.Contact, error)
	listReactionsFn  func(context.Context, string) ([]store.Reaction, error)
	listReceiptsFn   func(context.Context, string) ([]store.Receipt, error)
	getMediaRefFn    func(context.Context, string) (store.MediaRef, error)
}

func (f *fakeService) Status(ctx context.Context) (waclient.Status, error) {
	return f.statusFn(ctx)
}

func (f *fakeService) Stats(ctx context.Context) (service.Stats, error) {
	return f.statsFn(ctx)
}

func (f *fakeService) ListChats(ctx context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	return f.listChatsFn(ctx, before, limit, includeArchived)
}

func (f *fakeService) GetChat(ctx context.Context, jid string) (store.Chat, error) {
	return f.getChatFn(ctx, jid)
}

func (f *fakeService) ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error) {
	return f.listMessagesFn(ctx, chatJID, beforeTS, limit)
}

func (f *fakeService) SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error) {
	return f.searchMessagesFn(ctx, query, limit)
}

func (f *fakeService) ListContacts(ctx context.Context) ([]store.Contact, error) {
	return f.listContactsFn(ctx)
}

func (f *fakeService) SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error) {
	return f.searchContactsFn(ctx, query, limit)
}

func (f *fakeService) ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error) {
	return f.listReactionsFn(ctx, messageID)
}

func (f *fakeService) ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error) {
	return f.listReceiptsFn(ctx, messageID)
}

func (f *fakeService) GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error) {
	return f.getMediaRefFn(ctx, messageID)
}

func inMemoryClient(t *testing.T, svc service.Service) (context.Context, *mcpsdk.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := newServer(Deps{Service: svc})

	sTr, cTr := mcpsdk.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, sTr) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcp-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, cTr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return ctx, session
}

func TestWAStatus_HappyPath(t *testing.T) {
	jid := "1@s.whatsapp.net"
	svc := &fakeService{
		statusFn: func(_ context.Context) (waclient.Status, error) {
			return waclient.Status{Connected: true, JID: &jid}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_status"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waStatusOutput](t, res)
	require.True(t, out.Connected)
	require.Equal(t, "1@s.whatsapp.net", out.JID)
}

func TestWAStatus_ServiceError(t *testing.T) {
	svc := &fakeService{
		statusFn: func(_ context.Context) (waclient.Status, error) {
			return waclient.Status{}, errors.New("upstream gone")
		},
	}
	ctx, session := inMemoryClient(t, svc)

	// Unknown errors are wrapped by the SDK as tool errors (IsError=true),
	// not as protocol-level errors, so session.CallTool returns nil err.
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_status"})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWAStats_HappyPath(t *testing.T) {
	svc := &fakeService{
		statsFn: func(_ context.Context) (service.Stats, error) {
			return service.Stats{Chats: 7, Messages: 42, Contacts: 13, UnreadTotal: 3}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_stats"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waStatsOutput](t, res)
	require.Equal(t, 7, out.Chats)
	require.Equal(t, 42, out.Messages)
	require.Equal(t, 13, out.Contacts)
	require.Equal(t, 3, out.UnreadTotal)
}

// decodeStructured pulls the typed output from a CallToolResult.
// The SDK stores the output as json.RawMessage server-side, but after the
// in-memory transport JSON round-trip the client receives it as map[string]any.
// We re-marshal and unmarshal to get a typed value.
func decodeStructured[T any](t *testing.T, res *mcpsdk.CallToolResult) T {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "StructuredContent is nil")
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

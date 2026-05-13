package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// fakeService stubs service.Service for tool tests; each test wires only the
// fields it exercises. A nil function field panics on access — that surfaces
// missing wiring as a test bug instead of a confusing nil-method error.
type fakeService struct {
	service.Service // unset methods panic via embedded-nil deref
	statusFn func(context.Context) (waclient.Status, error)
	statsFn  func(context.Context) (service.Stats, error)
}

func (f *fakeService) Status(ctx context.Context) (waclient.Status, error) {
	return f.statusFn(ctx)
}

func (f *fakeService) Stats(ctx context.Context) (service.Stats, error) {
	return f.statsFn(ctx)
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
			return service.Stats{Chats: 7, Messages: 42}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_stats"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waStatsOutput](t, res)
	require.Equal(t, 7, out.Chats)
	require.Equal(t, 42, out.Messages)
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

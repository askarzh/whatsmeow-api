package mcp

import (
	"context"
	"fmt"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// --- wa_login_qr ---

func TestWALoginQR_ReturnsFirstEvent(t *testing.T) {
	ch := make(chan waclient.QREvent, 1)
	ch <- waclient.QREvent{Code: "qr-data-string", Terminal: false}

	svc := &fakeService{
		loginQRFn: func(_ context.Context) (<-chan waclient.QREvent, error) {
			return ch, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_login_qr"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waLoginQROutput](t, res)
	require.Equal(t, "qr-data-string", out.Code)
	require.False(t, out.Terminal)
}

func TestWALoginQR_TerminalEvent(t *testing.T) {
	ch := make(chan waclient.QREvent, 1)
	ch <- waclient.QREvent{Code: "", Terminal: true, Outcome: "paired"}

	svc := &fakeService{
		loginQRFn: func(_ context.Context) (<-chan waclient.QREvent, error) {
			return ch, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_login_qr"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waLoginQROutput](t, res)
	require.True(t, out.Terminal)
	require.Equal(t, "paired", out.Outcome)
}

func TestWALoginQR_ServiceError(t *testing.T) {
	svc := &fakeService{
		loginQRFn: func(_ context.Context) (<-chan waclient.QREvent, error) {
			return nil, fmt.Errorf("%w: already paired", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_login_qr"})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

// --- wa_login_phone ---

func TestWALoginPhone_HappyPath(t *testing.T) {
	ch := make(chan waclient.PairEvent, 1)
	ch <- waclient.PairEvent{Code: "ABCD-1234", Terminal: false}

	svc := &fakeService{
		loginPhoneFn: func(_ context.Context, phone string) (<-chan waclient.PairEvent, error) {
			require.Equal(t, "14155551212", phone)
			return ch, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_login_phone",
		Arguments: map[string]any{"phone_number": "14155551212"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waLoginPhoneOutput](t, res)
	require.Equal(t, "ABCD-1234", out.Code)
	require.False(t, out.Terminal)
}

func TestWALoginPhone_ServiceError(t *testing.T) {
	svc := &fakeService{
		loginPhoneFn: func(_ context.Context, _ string) (<-chan waclient.PairEvent, error) {
			return nil, fmt.Errorf("%w: login already in progress", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_login_phone",
		Arguments: map[string]any{"phone_number": "14155551212"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

// --- wa_logout ---

func TestWALogout_HappyPath(t *testing.T) {
	svc := &fakeService{
		logoutFn: func(_ context.Context) error {
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_logout"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

func TestWALogout_ServiceError(t *testing.T) {
	svc := &fakeService{
		logoutFn: func(_ context.Context) error {
			return fmt.Errorf("%w: not logged in", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_logout"})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

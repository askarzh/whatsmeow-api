package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/config"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
)

func TestStreamableHTTP_EndToEnd_HappyPath(t *testing.T) {
	router := httpapi.NewRouter(httpapi.Deps{
		Config: config.Config{
			Auth: config.AuthConfig{Token: "tok"},
			MCP:  config.MCPConfig{Enabled: true},
		},
		Service: integMinService{},
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: ts.URL + "/v1/mcp",
		HTTPClient: &http.Client{
			Transport: bearerRoundTripper{base: http.DefaultTransport, token: "tok"},
		},
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "it-test", Version: "0"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_status"})
	require.NoError(t, err)
	require.False(t, res.IsError)
}

func TestStreamableHTTP_MissingBearer_401(t *testing.T) {
	router := httpapi.NewRouter(httpapi.Deps{
		Config:  config.Config{Auth: config.AuthConfig{Token: "tok"}, MCP: config.MCPConfig{Enabled: true}},
		Service: integMinService{},
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
}

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (b bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

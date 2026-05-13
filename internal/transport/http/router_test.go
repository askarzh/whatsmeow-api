package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/config"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterHealthIsPublic(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{Auth: config.AuthConfig{Token: "s3cret"}}, Service: fakeStatusSvc{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRouterStatusRequiresAuth(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{Auth: config.AuthConfig{Token: "s3cret"}}, Service: fakeStatusSvc{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRouterAuthDisabledStatusOpen(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{}, Service: fakeStatusSvc{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRouter_MountsMCPWhenEnabled(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{
		Config: config.Config{
			Auth: config.AuthConfig{Token: "s3cret"},
			MCP:  config.MCPConfig{Enabled: true},
		},
		Service: fakeStatusSvc{},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusNotFound, w.Code, "expected /v1/mcp to be mounted; got 404")
}

func TestRouter_MCPDisabledReturns404(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{
		Config: config.Config{
			Auth: config.AuthConfig{Token: "s3cret"},
			MCP:  config.MCPConfig{Enabled: false},
		},
		Service: fakeStatusSvc{},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_MCPRequiresBearer(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{
		Config: config.Config{
			Auth: config.AuthConfig{Token: "s3cret"},
			MCP:  config.MCPConfig{Enabled: true},
		},
		Service: fakeStatusSvc{},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
	// no Authorization header
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
}

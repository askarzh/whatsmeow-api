package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/config"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
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

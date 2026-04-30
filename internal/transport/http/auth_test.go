package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthDisabledLetsThrough(t *testing.T) {
	mw := httpapi.RequireBearerToken("")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthEnabledMissingHeader(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestAuthEnabledWrongToken(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer nope")
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthEnabledRightToken(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

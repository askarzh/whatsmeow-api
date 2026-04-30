package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)

	httpapi.HealthHandler().ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
	_, hasDB := body["db"]
	_, hasWA := body["wa_connected"]
	assert.True(t, hasDB, "db field present")
	assert.True(t, hasWA, "wa_connected field present")
}

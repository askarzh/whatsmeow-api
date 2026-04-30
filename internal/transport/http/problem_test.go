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

func TestProblemWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteProblem(rr, http.StatusForbidden, "auth.unauthorized", "missing token")

	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))

	var p httpapi.Problem
	require.NoError(t, json.NewDecoder(res.Body).Decode(&p))
	assert.Equal(t, "auth.unauthorized", p.Code)
	assert.Equal(t, "missing token", p.Detail)
	assert.Equal(t, http.StatusForbidden, p.Status)
}

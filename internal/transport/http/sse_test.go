package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", res.Header.Get("Connection"))
}

func TestSSEWriteEvent(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteEvent(rr, "hello", map[string]string{"k": "v"}))
	got := rr.Body.String()
	// SSE frame: "event: hello\ndata: {\"k\":\"v\"}\n\n"
	assert.True(t, strings.HasPrefix(got, "event: hello\n"), "frame begins with event line, got %q", got)
	assert.Contains(t, got, "data: {\"k\":\"v\"}")
	assert.True(t, strings.HasSuffix(got, "\n\n"), "frame terminates with blank line")
}

func TestSSEWriteHeartbeat(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteHeartbeat(rr))
	assert.Equal(t, ": heartbeat\n\n", rr.Body.String())
}

func TestSSEWriteEventNonObject(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteEvent(rr, "x", "scalar"))
	assert.Contains(t, rr.Body.String(), "data: \"scalar\"")
}

// fakeNonFlushable proves SSEWriteEvent is tolerant of writers that don't implement http.Flusher.
type fakeNonFlushable struct {
	h http.Header
	b []byte
}

func (f *fakeNonFlushable) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}
func (f *fakeNonFlushable) Write(p []byte) (int, error) { f.b = append(f.b, p...); return len(p), nil }
func (f *fakeNonFlushable) WriteHeader(int)             {}

func TestSSEWriteEventNoFlusher(t *testing.T) {
	w := &fakeNonFlushable{}
	httpapi.SSEPrepare(w)
	require.NoError(t, httpapi.SSEWriteEvent(w, "x", 1))
	assert.Contains(t, string(w.b), "event: x")
}

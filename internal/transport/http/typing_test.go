package http_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTypingSvc is a thin alias so tests in this file use the same fake but
// don't collide with the one declared in receipts_test.go. Both files are in
// the same test package so we just reuse fakeReceiptsSvc.

func TestSendTypingHappyPath(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"composing"}`)
	res, err := http.Post(srv.URL+"/v1/chats/123456789@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "123456789@s.whatsapp.net", f.gotTypingJID)
	assert.Equal(t, "composing", f.gotTypingState)
}

func TestSendTypingBadJSON(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/chats/123@s.whatsapp.net/typing", "application/json", bytes.NewBufferString("not-json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTypingBadState(t *testing.T) {
	f := &fakeReceiptsSvc{sendTypingErr: service.ErrInvalidRequest}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"invalid"}`)
	res, err := http.Post(srv.URL+"/v1/chats/123@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTypingNotConnectedHTTP(t *testing.T) {
	f := &fakeReceiptsSvc{sendTypingErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"composing"}`)
	res, err := http.Post(srv.URL+"/v1/chats/123@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

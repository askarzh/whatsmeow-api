package client_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/status", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer t", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"wa_connected":true,"jid":"j@s.whatsapp.net","push_name":"a","since":"2026-04-30T11:23:45Z"}`)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "t")
	st, err := c.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, st.WAConnected)
	assert.Equal(t, "j@s.whatsapp.net", st.JID)
	assert.Equal(t, "a", st.PushName)
}

func TestLogoutHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/logout", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	err := c.Logout(context.Background())
	assert.NoError(t, err)
}

func TestLogoutNotLoggedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"code":"wa.not_logged_in","detail":"x"}`)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	err := c.Logout(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, client.ErrNotLoggedIn)
}

func TestLoginQRStream(t *testing.T) {
	body := "event: qr\ndata: {\"code\":\"2@a\"}\n\nevent: qr\ndata: {\"code\":\"2@b\"}\n\nevent: connection\ndata: {\"outcome\":\"success\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	ch, err := c.LoginQR(context.Background())
	require.NoError(t, err)

	var got []client.QREvent
	for ev := range ch {
		got = append(got, ev)
	}
	require.Len(t, got, 3)
	assert.Equal(t, "2@a", got[0].Code)
	assert.Equal(t, "2@b", got[1].Code)
	assert.True(t, got[2].Terminal)
	assert.Equal(t, "success", got[2].Outcome)
}

func TestLoginPhoneStream(t *testing.T) {
	body := "event: pair_code\ndata: {\"code\":\"ABCD-1234\"}\n\nevent: connection\ndata: {\"outcome\":\"success\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/login/phone", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	ch, err := c.LoginPhone(context.Background(), "+27821234567")
	require.NoError(t, err)

	var got []client.PairEvent
	for ev := range ch {
		got = append(got, ev)
	}
	require.Len(t, got, 2)
	assert.Equal(t, "ABCD-1234", got[0].Code)
	assert.True(t, got[1].Terminal)
	assert.Equal(t, "success", got[1].Outcome)
}

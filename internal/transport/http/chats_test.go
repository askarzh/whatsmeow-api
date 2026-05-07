package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeChatsSvc struct {
	listResp    []store.Chat
	listErr     error
	gotBefore   time.Time
	gotLimit    int
	gotInclArch bool

	getResp store.Chat
	getErr  error
	gotJID  string

	listMsgsResp   []store.Message
	listMsgsErr    error
	gotMsgsChatJID string
	gotMsgsBefore  time.Time
	gotMsgsLimit   int
}

func (f *fakeChatsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeChatsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f *fakeChatsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f *fakeChatsSvc) Logout(context.Context) error                                          { return nil }
func (f *fakeChatsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeChatsSvc) ListChats(_ context.Context, before time.Time, limit int, inclArch bool) ([]store.Chat, error) {
	f.gotBefore = before
	f.gotLimit = limit
	f.gotInclArch = inclArch
	return f.listResp, f.listErr
}
func (f *fakeChatsSvc) GetChat(_ context.Context, jid string) (store.Chat, error) {
	f.gotJID = jid
	return f.getResp, f.getErr
}
func (f *fakeChatsSvc) ListMessages(_ context.Context, chatJID string, before time.Time, limit int) ([]store.Message, error) {
	f.gotMsgsChatJID = chatJID
	f.gotMsgsBefore = before
	f.gotMsgsLimit = limit
	return f.listMsgsResp, f.listMsgsErr
}
func (f *fakeChatsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeChatsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeChatsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeChatsSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f *fakeChatsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeChatsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeChatsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeChatsSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeChatsSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f *fakeChatsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}

var _ service.Service = (*fakeChatsSvc)(nil)

func TestListChatsHappyPath(t *testing.T) {
	f := &fakeChatsSvc{listResp: []store.Chat{
		{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
	}}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?limit=10&before=2026-05-02T00:00:00Z&include_archived=true")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Chats []map[string]any `json:"chats"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Chats, 1)
	assert.Equal(t, "a@s.whatsapp.net", body.Chats[0]["jid"])

	assert.Equal(t, 10, f.gotLimit)
	assert.True(t, f.gotInclArch)
	assert.False(t, f.gotBefore.IsZero())
}

func TestListChatsDefaults(t *testing.T) {
	f := &fakeChatsSvc{}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 50, f.gotLimit)
	assert.False(t, f.gotInclArch)
	assert.True(t, f.gotBefore.IsZero())
}

func TestListChatsValidation(t *testing.T) {
	f := &fakeChatsSvc{}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	cases := []struct{ q, label string }{
		{"limit=foo", "non-int limit"},
		{"limit=0", "limit too small"},
		{"limit=101", "limit too large"},
		{"before=garbage", "bad timestamp"},
		{"include_archived=maybe", "bad bool"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + tc.q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

func TestGetChatHappyPath(t *testing.T) {
	f := &fakeChatsSvc{getResp: store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user"}}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "a@s.whatsapp.net", f.gotJID)
}

func TestGetChatNotFound(t *testing.T) {
	f := &fakeChatsSvc{getErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/missing@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestGetChatInternalError(t *testing.T) {
	f := &fakeChatsSvc{getErr: errors.New("boom")}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

func TestListMessagesByChatHappyPath(t *testing.T) {
	f := &fakeChatsSvc{listMsgsResp: []store.Message{
		{ID: "M1", ChatJID: "a@s.whatsapp.net", Body: "hi", Timestamp: time.Now()},
	}}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}/messages", httpapi.ListMessagesByChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net/messages?limit=20")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "a@s.whatsapp.net", f.gotMsgsChatJID)
	assert.Equal(t, 20, f.gotMsgsLimit)
}

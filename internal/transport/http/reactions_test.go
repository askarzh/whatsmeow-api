package http_test

import (
	"bytes"
	"context"
	"encoding/json"
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

type fakeReactionsSvc struct {
	sendErr  error
	listResp []store.Reaction
	listErr  error

	gotMessageID string
	gotEmoji     string
	gotListID    string
}

func (f *fakeReactionsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeReactionsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) Logout(context.Context) error { return nil }
func (f *fakeReactionsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeReactionsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeReactionsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeReactionsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeReactionsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeReactionsSvc) SendReaction(_ context.Context, messageID, emoji string) error {
	f.gotMessageID = messageID
	f.gotEmoji = emoji
	return f.sendErr
}
func (f *fakeReactionsSvc) ListReactions(_ context.Context, messageID string) ([]store.Reaction, error) {
	f.gotListID = messageID
	return f.listResp, f.listErr
}
func (f *fakeReactionsSvc) MarkMessageRead(context.Context, string) error               { return nil }
func (f *fakeReactionsSvc) SendTyping(context.Context, string, string) error            { return nil }
func (f *fakeReactionsSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) { return nil, nil }

var _ service.Service = (*fakeReactionsSvc)(nil)

func TestSendReactionHappyPath(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotMessageID)
	assert.Equal(t, "👍", f.gotEmoji)
}

func TestSendReactionEmptyClears(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":""}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "", f.gotEmoji)
}

func TestSendReactionBadJSON(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendReactionNotFound(t *testing.T) {
	f := &fakeReactionsSvc{sendErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestSendReactionNotConnected(t *testing.T) {
	f := &fakeReactionsSvc{sendErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestListReactionsHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeReactionsSvc{listResp: []store.Reaction{
		{MessageID: "MID1", SenderJID: "a@s.whatsapp.net", Emoji: "👍", Timestamp: now},
	}}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/reactions", httpapi.ListReactionsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/reactions")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Reactions []map[string]any `json:"reactions"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Reactions, 1)
	assert.Equal(t, "MID1", body.Reactions[0]["message_id"])
	assert.Equal(t, "a@s.whatsapp.net", body.Reactions[0]["sender_jid"])
	assert.Equal(t, "👍", body.Reactions[0]["emoji"])
}

func TestListReactionsEmpty(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/reactions", httpapi.ListReactionsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/reactions")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Reactions []map[string]any `json:"reactions"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Empty(t, body.Reactions)
}

package http_test

import (
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

// fakeReceiptsSvc is a full service.Service stub for receipts + typing tests.
type fakeReceiptsSvc struct {
	markReadErr  error
	listResp     []store.Receipt
	listErr      error
	sendTypingErr error

	gotMarkReadID   string
	gotListID       string
	gotTypingJID    string
	gotTypingState  string
}

func (f *fakeReceiptsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeReceiptsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) Logout(context.Context) error { return nil }
func (f *fakeReceiptsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeReceiptsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeReceiptsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeReceiptsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeReceiptsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeReceiptsSvc) SendReaction(context.Context, string, string) error { return nil }
func (f *fakeReceiptsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) MarkMessageRead(_ context.Context, messageID string) error {
	f.gotMarkReadID = messageID
	return f.markReadErr
}
func (f *fakeReceiptsSvc) SendTyping(_ context.Context, chatJID, state string) error {
	f.gotTypingJID = chatJID
	f.gotTypingState = state
	return f.sendTypingErr
}
func (f *fakeReceiptsSvc) ListReceipts(_ context.Context, messageID string) ([]store.Receipt, error) {
	f.gotListID = messageID
	return f.listResp, f.listErr
}
func (f *fakeReceiptsSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}

var _ service.Service = (*fakeReceiptsSvc)(nil)

// ---- MarkRead tests ----

func TestMarkReadHappyPath(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotMarkReadID)
}

func TestMarkReadNotFound(t *testing.T) {
	f := &fakeReceiptsSvc{markReadErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MISSING/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestMarkReadNotConnected(t *testing.T) {
	f := &fakeReceiptsSvc{markReadErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

// ---- ListReceipts tests ----

func TestListReceiptsHandlerHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeReceiptsSvc{listResp: []store.Receipt{
		{MessageID: "MID1", ReaderJID: "a@s.whatsapp.net", Type: "read", Timestamp: now},
		{MessageID: "MID1", ReaderJID: "b@s.whatsapp.net", Type: "delivered", Timestamp: now},
	}}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/receipts", httpapi.ListReceiptsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/receipts")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "MID1", f.gotListID)

	var body struct {
		Receipts []map[string]any `json:"receipts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Receipts, 2)
	assert.Equal(t, "MID1", body.Receipts[0]["message_id"])
	assert.Equal(t, "a@s.whatsapp.net", body.Receipts[0]["reader_jid"])
	assert.Equal(t, "read", body.Receipts[0]["type"])
	assert.Equal(t, "2026-05-07T12:00:00Z", body.Receipts[0]["ts"])
}

func TestListReceiptsHandlerEmpty(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/receipts", httpapi.ListReceiptsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/receipts")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Receipts []map[string]any `json:"receipts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Empty(t, body.Receipts)
}

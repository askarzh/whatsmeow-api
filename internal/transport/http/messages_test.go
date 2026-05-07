package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat    string
	gotText    string
	gotReplyTo string // Plan 07a

	editResp    store.Message
	editErr     error
	gotEditID   string
	gotEditText string

	deleteErr   error
	gotDeleteID string

	// Plan 05 search capture
	searchResp     []store.Message
	searchErr      error
	gotSearchQ     string
	gotSearchLimit int
}

func (f *fakeSendSvc) Status(context.Context) (waclient.Status, error) { return waclient.Status{}, nil }
func (f *fakeSendSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) Logout(context.Context) error { return nil }
func (f *fakeSendSvc) SendText(_ context.Context, chat, text, replyTo string) (store.Message, error) {
	f.gotChat = chat
	f.gotText = text
	f.gotReplyTo = replyTo
	return f.resp, f.err
}
func (f *fakeSendSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeSendSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeSendSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeSendSvc) SearchMessages(_ context.Context, q string, limit int) ([]store.Message, error) {
	f.gotSearchQ = q
	f.gotSearchLimit = limit
	return f.searchResp, f.searchErr
}
func (f *fakeSendSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeSendSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeSendSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f *fakeSendSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeSendSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeSendSvc) EditMessage(_ context.Context, id, text string) (store.Message, error) {
	f.gotEditID = id
	f.gotEditText = text
	return f.editResp, f.editErr
}
func (f *fakeSendSvc) DeleteMessage(_ context.Context, id string) error {
	f.gotDeleteID = id
	return f.deleteErr
}
func (f *fakeSendSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f *fakeSendSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}

var _ service.Service = (*fakeSendSvc)(nil)

func TestSendTextHappyPath(t *testing.T) {
	ts := time.Date(2026, 5, 1, 12, 34, 56, 0, time.UTC)
	f := &fakeSendSvc{resp: store.Message{
		ID: "MID1", ChatJID: "27821234567@s.whatsapp.net", Timestamp: ts,
		Kind: "text", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"chat_jid":"27821234567@s.whatsapp.net","text":"hi"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()

	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, "27821234567@s.whatsapp.net", f.gotChat)
	assert.Equal(t, "hi", f.gotText)

	var got struct {
		ID      string    `json:"id"`
		ChatJID string    `json:"chat_jid"`
		Ts      time.Time `json:"ts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "27821234567@s.whatsapp.net", got.ChatJID)
	assert.True(t, got.Ts.Equal(ts))
}

func TestSendTextRejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(`not json`))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
}

func TestSendTextRejectsEmptyText(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(`{"chat_jid":"a@s.whatsapp.net","text":""}`))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextRejectsLongText(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"` + strings.Repeat("x", 4097) + `"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextRejectsValidationFromService(t *testing.T) {
	// Service returns ErrInvalidRequest -> 400.
	f := &fakeSendSvc{err: service.ErrInvalidRequest}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextNotConnected(t *testing.T) {
	f := &fakeSendSvc{err: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestSearchMessagesHappyPath(t *testing.T) {
	f := &fakeSendSvc{searchResp: []store.Message{
		{ID: "M1", ChatJID: "c@s.whatsapp.net", Body: "fox", Timestamp: time.Now()},
	}}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=fox&limit=20")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Messages, 1)
	assert.Equal(t, "M1", body.Messages[0]["id"])
	assert.Equal(t, "fox", f.gotSearchQ)
	assert.Equal(t, 20, f.gotSearchLimit)
}

func TestSearchMessagesDefaultLimit(t *testing.T) {
	f := &fakeSendSvc{}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=anything")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 50, f.gotSearchLimit)
}

func TestSearchMessagesValidation(t *testing.T) {
	f := &fakeSendSvc{}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	cases := []string{"", "limit=0", "q=x&limit=foo", "q=x&limit=101"}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

func TestSendTextInternalError(t *testing.T) {
	f := &fakeSendSvc{err: errors.New("boom")}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

func TestSendTextWithReplyToField(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeSendSvc{resp: store.Message{
		ID: "MID1", ChatJID: "c@s.whatsapp.net", Timestamp: now, Kind: "text", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"chat_jid":"c@s.whatsapp.net","text":"hi","reply_to":"PARENT_ID"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, "PARENT_ID", f.gotReplyTo)
}

func TestEditMessageHappyPath(t *testing.T) {
	editedAt := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeSendSvc{editResp: store.Message{
		ID: "MID1", ChatJID: "c@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind: "text", Body: "new", EditedAt: &editedAt,
	}}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "MID1", f.gotEditID)
	assert.Equal(t, "new", f.gotEditText)
}

func TestEditMessageEmptyText(t *testing.T) {
	f := &fakeSendSvc{}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":""}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestEditMessageForbidden(t *testing.T) {
	f := &fakeSendSvc{editErr: service.ErrForbidden}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestEditMessageNotFound(t *testing.T) {
	f := &fakeSendSvc{editErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestEditMessageNotConnected(t *testing.T) {
	f := &fakeSendSvc{editErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestDeleteMessageHappyPath(t *testing.T) {
	f := &fakeSendSvc{}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotDeleteID)
}

func TestDeleteMessageForbidden(t *testing.T) {
	f := &fakeSendSvc{deleteErr: service.ErrForbidden}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestDeleteMessageNotFound(t *testing.T) {
	f := &fakeSendSvc{deleteErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestDeleteMessageNotConnected(t *testing.T) {
	f := &fakeSendSvc{deleteErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

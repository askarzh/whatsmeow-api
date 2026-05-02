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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat string
	gotText string
}

func (f *fakeSendSvc) Status(context.Context) (waclient.Status, error) { return waclient.Status{}, nil }
func (f *fakeSendSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) Logout(context.Context) error { return nil }
func (f *fakeSendSvc) SendText(_ context.Context, chat, text string) (store.Message, error) {
	f.gotChat = chat
	f.gotText = text
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
func (f *fakeSendSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeSendSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeSendSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeSendSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

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

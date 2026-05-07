package http_test

import (
	"bytes"
	"context"
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

type fakeLoginPhoneSvc struct {
	ch     <-chan waclient.PairEvent
	err    error
	gotNum string
}

func (f *fakeLoginPhoneSvc) Status(context.Context) (waclient.Status, error)        { return waclient.Status{}, nil }
func (f *fakeLoginPhoneSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) { return nil, nil }
func (f *fakeLoginPhoneSvc) LoginPhone(_ context.Context, n string) (<-chan waclient.PairEvent, error) {
	f.gotNum = n
	return f.ch, f.err
}
func (f *fakeLoginPhoneSvc) Logout(context.Context) error { return nil }
func (f *fakeLoginPhoneSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeLoginPhoneSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeLoginPhoneSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f *fakeLoginPhoneSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeLoginPhoneSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeLoginPhoneSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeLoginPhoneSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeLoginPhoneSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f *fakeLoginPhoneSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeLoginPhoneSvc) MarkMessageRead(context.Context, string) error               { return nil }
func (f *fakeLoginPhoneSvc) SendTyping(context.Context, string, string) error            { return nil }
func (f *fakeLoginPhoneSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) { return nil, nil }
func (f *fakeLoginPhoneSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}

var _ service.Service = (*fakeLoginPhoneSvc)(nil)

func TestLoginPhoneStreamsCodeThenSuccess(t *testing.T) {
	ch := make(chan waclient.PairEvent, 2)
	ch <- waclient.PairEvent{Code: "ABCD-1234"}
	ch <- waclient.PairEvent{Terminal: true, Outcome: "success"}
	close(ch)

	f := &fakeLoginPhoneSvc{ch: ch}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := strings.NewReader(`{"phone_number":"+27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	assert.Equal(t, "+27821234567", f.gotNum)

	frames := readSSEFrames(t, res)
	require.GreaterOrEqual(t, len(frames), 2)
	assert.Equal(t, "pair_code", frames[0].event)
	assert.Contains(t, frames[0].data, `"code":"ABCD-1234"`)
	assert.Equal(t, "connection", frames[1].event)
	assert.Contains(t, frames[1].data, `"outcome":"success"`)
}

func TestLoginPhoneHandlerRejectsBadNumber(t *testing.T) {
	f := &fakeLoginPhoneSvc{}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"phone_number":"27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
	assert.Empty(t, f.gotNum)
}

func TestLoginPhoneRejectsMalformedJSON(t *testing.T) {
	f := &fakeLoginPhoneSvc{}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`not json`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLoginPhoneConflict(t *testing.T) {
	f := &fakeLoginPhoneSvc{err: waclient.ErrLoginInProgress}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := strings.NewReader(`{"phone_number":"+27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

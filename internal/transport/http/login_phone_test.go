package http_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
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

package http_test

import (
	"bufio"
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

type fakeLoginQRSvc struct {
	ch  <-chan waclient.QREvent
	err error
}

func (f fakeLoginQRSvc) Status(context.Context) (waclient.Status, error)        { return waclient.Status{}, nil }
func (f fakeLoginQRSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) { return f.ch, f.err }
func (f fakeLoginQRSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f fakeLoginQRSvc) Logout(context.Context) error { return nil }
func (f fakeLoginQRSvc) SendText(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeLoginQRSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f fakeLoginQRSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f fakeLoginQRSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeLoginQRSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeLoginQRSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return nil, nil
}
func (f fakeLoginQRSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}

var _ service.Service = fakeLoginQRSvc{}

func TestLoginQRStreamsCodesThenSuccess(t *testing.T) {
	ch := make(chan waclient.QREvent, 3)
	ch <- waclient.QREvent{Code: "2@first"}
	ch <- waclient.QREvent{Code: "2@second"}
	ch <- waclient.QREvent{Terminal: true, Outcome: "success"}
	close(ch)

	srv := httptest.NewServer(httpapi.LoginQRHandler(fakeLoginQRSvc{ch: ch}))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

	frames := readSSEFrames(t, res)
	require.GreaterOrEqual(t, len(frames), 3)
	assert.Equal(t, "qr", frames[0].event)
	assert.Contains(t, frames[0].data, `"code":"2@first"`)
	assert.Equal(t, "qr", frames[1].event)
	assert.Contains(t, frames[1].data, `"code":"2@second"`)
	assert.Equal(t, "connection", frames[2].event)
	assert.Contains(t, frames[2].data, `"outcome":"success"`)
}

func TestLoginQRConflict(t *testing.T) {
	srv := httptest.NewServer(httpapi.LoginQRHandler(fakeLoginQRSvc{err: waclient.ErrAlreadyLoggedIn}))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
}

type sseFrame struct{ event, data string }

func readSSEFrames(t *testing.T, res *http.Response) []sseFrame {
	t.Helper()
	var out []sseFrame
	cur := sseFrame{}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" || cur.data != "" {
				out = append(out, cur)
				cur = sseFrame{}
			}
		}
	}
	return out
}

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStatsSvc struct{ resp service.Stats }

func (f fakeStatsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f fakeStatsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f fakeStatsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f fakeStatsSvc) Logout(context.Context) error                                         { return nil }
func (f fakeStatsSvc) SendText(context.Context, string, string) (store.Message, error)       { return store.Message{}, nil }
func (f fakeStatsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) { return nil, nil }
func (f fakeStatsSvc) GetChat(context.Context, string) (store.Chat, error)                   { return store.Chat{}, nil }
func (f fakeStatsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error)  { return nil, nil }
func (f fakeStatsSvc) ListContacts(context.Context) ([]store.Contact, error)                 { return nil, nil }
func (f fakeStatsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error)  { return nil, nil }
func (f fakeStatsSvc) Stats(context.Context) (service.Stats, error)                          { return f.resp, nil }

func (f fakeStatsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeStatsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}

var _ service.Service = fakeStatsSvc{}

func TestStatsHappyPath(t *testing.T) {
	srv := httptest.NewServer(httpapi.StatsHandler(fakeStatsSvc{
		resp: service.Stats{Chats: 12, Messages: 4567, Contacts: 8, UnreadTotal: 3},
	}))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Chats       int `json:"chats"`
		Messages    int `json:"messages"`
		Contacts    int `json:"contacts"`
		UnreadTotal int `json:"unread_total"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, 12, body.Chats)
	assert.Equal(t, 4567, body.Messages)
	assert.Equal(t, 8, body.Contacts)
	assert.Equal(t, 3, body.UnreadTotal)
}

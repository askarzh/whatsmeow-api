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

type fakeContactsSvc struct {
	listResp     []store.Contact
	searchResp   []store.Contact
	searchErr    error
	gotSearchQ   string
	gotSearchLim int
}

func (f *fakeContactsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeContactsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeContactsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeContactsSvc) Logout(context.Context) error { return nil }
func (f *fakeContactsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeContactsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeContactsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeContactsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeContactsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeContactsSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return f.listResp, nil
}
func (f *fakeContactsSvc) SearchContacts(_ context.Context, q string, limit int) ([]store.Contact, error) {
	f.gotSearchQ = q
	f.gotSearchLim = limit
	return f.searchResp, f.searchErr
}
func (f *fakeContactsSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f *fakeContactsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeContactsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeContactsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeContactsSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeContactsSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f *fakeContactsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeContactsSvc) MarkMessageRead(context.Context, string) error    { return nil }
func (f *fakeContactsSvc) SendTyping(context.Context, string, string) error { return nil }
func (f *fakeContactsSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}
func (f *fakeContactsSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeContactsSvc) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f *fakeContactsSvc) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f *fakeContactsSvc) LeaveGroup(context.Context, string) error { return nil }

var _ service.Service = (*fakeContactsSvc)(nil)

func TestListContactsHappyPath(t *testing.T) {
	f := &fakeContactsSvc{listResp: []store.Contact{{JID: "a@s.whatsapp.net", PushName: "A"}}}
	srv := httptest.NewServer(httpapi.ListContactsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Contacts []map[string]any `json:"contacts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Contacts, 1)
	assert.Equal(t, "a@s.whatsapp.net", body.Contacts[0]["jid"])
}

func TestSearchContactsHappyPath(t *testing.T) {
	f := &fakeContactsSvc{searchResp: []store.Contact{{JID: "a@s.whatsapp.net", PushName: "Alice"}}}
	srv := httptest.NewServer(httpapi.SearchContactsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=ali&limit=10")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Contacts []map[string]any `json:"contacts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Contacts, 1)
	assert.Equal(t, "ali", f.gotSearchQ)
	assert.Equal(t, 10, f.gotSearchLim)
}

func TestSearchContactsValidation(t *testing.T) {
	f := &fakeContactsSvc{}
	srv := httptest.NewServer(httpapi.SearchContactsHandler(f))
	defer srv.Close()

	for _, q := range []string{"", "limit=0", "q=x&limit=101"} {
		t.Run(q, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

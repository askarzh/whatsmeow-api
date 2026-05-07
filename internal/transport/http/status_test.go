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

type fakeStatusSvc struct{ st waclient.Status }

func (f fakeStatusSvc) Status(context.Context) (waclient.Status, error)          { return f.st, nil }
func (f fakeStatusSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) { return nil, nil }
func (f fakeStatusSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f fakeStatusSvc) Logout(context.Context) error { return nil }
func (f fakeStatusSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeStatusSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f fakeStatusSvc) GetChat(context.Context, string) (store.Chat, error) { return store.Chat{}, nil }
func (f fakeStatusSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatusSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatusSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f fakeStatusSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f fakeStatusSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f fakeStatusSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeStatusSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f fakeStatusSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeStatusSvc) DeleteMessage(context.Context, string) error { return nil }
func (f fakeStatusSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f fakeStatusSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f fakeStatusSvc) MarkMessageRead(context.Context, string) error    { return nil }
func (f fakeStatusSvc) SendTyping(context.Context, string, string) error { return nil }
func (f fakeStatusSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}
func (f fakeStatusSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f fakeStatusSvc) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f fakeStatusSvc) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f fakeStatusSvc) LeaveGroup(context.Context, string) error { return nil }

var _ service.Service = fakeStatusSvc{}

func TestStatusHandlerDisconnected(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	httpapi.StatusHandler(fakeStatusSvc{st: waclient.Status{}}).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, false, body["wa_connected"])
	assert.Nil(t, body["jid"])
	assert.Nil(t, body["push_name"])
	assert.Nil(t, body["since"])
}

func TestStatusHandlerConnected(t *testing.T) {
	jid := "27821234567@s.whatsapp.net"
	push := "Askar"
	since := time.Date(2026, 4, 30, 11, 23, 45, 0, time.UTC)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	httpapi.StatusHandler(fakeStatusSvc{st: waclient.Status{
		Connected: true, JID: &jid, PushName: &push, Since: &since,
	}}).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, true, body["wa_connected"])
	assert.Equal(t, jid, body["jid"])
	assert.Equal(t, push, body["push_name"])
	assert.Equal(t, "2026-04-30T11:23:45Z", body["since"])
}

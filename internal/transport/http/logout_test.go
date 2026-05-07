package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
)

type fakeLogoutSvc struct{ err error }

func (f fakeLogoutSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f fakeLogoutSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) { return nil, nil }
func (f fakeLogoutSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f fakeLogoutSvc) Logout(context.Context) error { return f.err }
func (f fakeLogoutSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeLogoutSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f fakeLogoutSvc) GetChat(context.Context, string) (store.Chat, error) { return store.Chat{}, nil }
func (f fakeLogoutSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeLogoutSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeLogoutSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f fakeLogoutSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f fakeLogoutSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

func (f fakeLogoutSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeLogoutSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f fakeLogoutSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeLogoutSvc) DeleteMessage(context.Context, string) error { return nil }
func (f fakeLogoutSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f fakeLogoutSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f fakeLogoutSvc) MarkMessageRead(context.Context, string) error    { return nil }
func (f fakeLogoutSvc) SendTyping(context.Context, string, string) error { return nil }
func (f fakeLogoutSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}
func (f fakeLogoutSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f fakeLogoutSvc) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f fakeLogoutSvc) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f fakeLogoutSvc) LeaveGroup(context.Context, string) error { return nil }

var _ service.Service = fakeLogoutSvc{}

func TestLogoutSuccess(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logout", nil)
	httpapi.LogoutHandler(fakeLogoutSvc{}).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestLogoutNotLoggedIn(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logout", nil)
	httpapi.LogoutHandler(fakeLogoutSvc{err: waclient.ErrNotLoggedIn}).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

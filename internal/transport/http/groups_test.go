package http_test

import (
	"bytes"
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

type fakeGroupsSvc struct {
	createResp waclient.Group
	createErr  error
	infoResp   []waclient.GroupMember
	infoErr    error
	updateResp []waclient.ParticipantChange
	updateErr  error
	leaveErr   error

	gotCreateName   string
	gotCreateParts  []string
	gotInfoJID      string
	gotUpdateGroup  string
	gotUpdateAction string
	gotUpdateParts  []string
	gotLeaveJID     string
}

func (f *fakeGroupsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeGroupsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) Logout(context.Context) error { return nil }
func (f *fakeGroupsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeGroupsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeGroupsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeGroupsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeGroupsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) DeleteMessage(context.Context, string) error              { return nil }
func (f *fakeGroupsSvc) SendReaction(context.Context, string, string) error       { return nil }
func (f *fakeGroupsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) MarkMessageRead(context.Context, string) error    { return nil }
func (f *fakeGroupsSvc) SendTyping(context.Context, string, string) error { return nil }
func (f *fakeGroupsSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}

func (f *fakeGroupsSvc) CreateGroup(_ context.Context, name string, parts []string) (waclient.Group, error) {
	f.gotCreateName = name
	f.gotCreateParts = parts
	return f.createResp, f.createErr
}
func (f *fakeGroupsSvc) ListGroupMembers(_ context.Context, jid string) ([]waclient.GroupMember, error) {
	f.gotInfoJID = jid
	return f.infoResp, f.infoErr
}
func (f *fakeGroupsSvc) UpdateGroupMembers(_ context.Context, jid, action string, parts []string) ([]waclient.ParticipantChange, error) {
	f.gotUpdateGroup = jid
	f.gotUpdateAction = action
	f.gotUpdateParts = parts
	return f.updateResp, f.updateErr
}
func (f *fakeGroupsSvc) LeaveGroup(_ context.Context, jid string) error {
	f.gotLeaveJID = jid
	return f.leaveErr
}

var _ service.Service = (*fakeGroupsSvc)(nil)

func TestCreateGroupHTTPHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeGroupsSvc{createResp: waclient.Group{
		JID: "g1@g.us", Name: "Test", OwnerJID: "me@s.whatsapp.net",
		CreatedAt: now,
		Participants: []waclient.GroupMember{
			{JID: "me@s.whatsapp.net", IsAdmin: true, IsSuperAdmin: true},
			{JID: "alice@s.whatsapp.net"},
		},
	}}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"name":"Test","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	assert.Equal(t, "Test", f.gotCreateName)
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, f.gotCreateParts)

	var resp struct {
		JID     string           `json:"jid"`
		Name    string           `json:"name"`
		Members []map[string]any `json:"members"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	assert.Equal(t, "g1@g.us", resp.JID)
	assert.Equal(t, "Test", resp.Name)
	assert.Len(t, resp.Members, 2)
}

func TestCreateGroupHTTPBadJSON(t *testing.T) {
	f := &fakeGroupsSvc{}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCreateGroupHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{createErr: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"name":"Test","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestListGroupMembersHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{infoResp: []waclient.GroupMember{
		{JID: "alice@s.whatsapp.net", IsAdmin: true},
	}}
	r := chi.NewRouter()
	r.Get("/v1/groups/{jid}/members", httpapi.ListGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/groups/g1@g.us/members")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotInfoJID)

	var body struct {
		Members []map[string]any `json:"members"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Members, 1)
	assert.Equal(t, "alice@s.whatsapp.net", body.Members[0]["jid"])
	assert.Equal(t, true, body.Members[0]["is_admin"])
}

func TestListGroupMembersHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{infoErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Get("/v1/groups/{jid}/members", httpapi.ListGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/groups/g1@g.us/members")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestUpdateGroupMembersHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{updateResp: []waclient.ParticipantChange{
		{JID: "alice@s.whatsapp.net", OK: true},
	}}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"action":"add","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotUpdateGroup)
	assert.Equal(t, "add", f.gotUpdateAction)

	var resp struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	require.Len(t, resp.Results, 1)
	assert.Equal(t, true, resp.Results[0]["ok"])
}

func TestUpdateGroupMembersHTTPBadJSON(t *testing.T) {
	f := &fakeGroupsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestUpdateGroupMembersHTTPBadAction(t *testing.T) {
	f := &fakeGroupsSvc{updateErr: service.ErrInvalidRequest}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"action":"yelling","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLeaveGroupHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{}
	r := chi.NewRouter()
	r.Delete("/v1/groups/{jid}/membership", httpapi.LeaveGroupHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/groups/g1@g.us/membership", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotLeaveJID)
}

func TestLeaveGroupHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{leaveErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Delete("/v1/groups/{jid}/membership", httpapi.LeaveGroupHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/groups/g1@g.us/membership", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

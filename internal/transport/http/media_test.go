package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
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

type fakeMediaSvc struct {
	sendResp store.Message
	sendErr  error
	getResp  store.MediaRef
	getErr   error

	gotReq       service.SendMediaRequest
	gotMessageID string
}

func (f *fakeMediaSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeMediaSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeMediaSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeMediaSvc) Logout(context.Context) error { return nil }
func (f *fakeMediaSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeMediaSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeMediaSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeMediaSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeMediaSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeMediaSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeMediaSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeMediaSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeMediaSvc) SendMedia(_ context.Context, req service.SendMediaRequest) (store.Message, error) {
	f.gotReq = req
	return f.sendResp, f.sendErr
}
func (f *fakeMediaSvc) GetMediaRef(_ context.Context, id string) (store.MediaRef, error) {
	f.gotMessageID = id
	return f.getResp, f.getErr
}
func (f *fakeMediaSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeMediaSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeMediaSvc) SendReaction(context.Context, string, string) error {
	return nil
}
func (f *fakeMediaSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeMediaSvc) MarkMessageRead(context.Context, string) error               { return nil }
func (f *fakeMediaSvc) SendTyping(context.Context, string, string) error            { return nil }
func (f *fakeMediaSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) { return nil, nil }
func (f *fakeMediaSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeMediaSvc) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f *fakeMediaSvc) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f *fakeMediaSvc) LeaveGroup(context.Context, string) error { return nil }

var _ service.Service = (*fakeMediaSvc)(nil)

type filePart struct {
	field, filename, contentType string
	body                         []byte
}

func makeMultipart(t *testing.T, fields map[string]string, file *filePart) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	if file != nil {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", `form-data; name="`+file.field+`"; filename="`+file.filename+`"`)
		hdr.Set("Content-Type", file.contentType)
		fw, err := w.CreatePart(hdr)
		require.NoError(t, err)
		_, err = fw.Write(file.body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf, w.FormDataContentType()
}

func TestSendMediaHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	f := &fakeMediaSvc{sendResp: store.Message{
		ID: "MID1", ChatJID: "27821234567@s.whatsapp.net", Timestamp: now, Kind: "image", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	imageBytes := []byte("fake-image-bytes")
	body, ct := makeMultipart(t,
		map[string]string{
			"chat_jid": "27821234567@s.whatsapp.net",
			"kind":     "image",
			"caption":  "hi",
		},
		&filePart{field: "file", filename: "img.jpg", contentType: "image/jpeg", body: imageBytes},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	assert.Equal(t, "27821234567@s.whatsapp.net", f.gotReq.ChatJID)
	assert.Equal(t, "image", f.gotReq.Kind)
	assert.Equal(t, "hi", f.gotReq.Caption)
	assert.Equal(t, "image/jpeg", f.gotReq.MIME)
	assert.Equal(t, imageBytes, f.gotReq.Body)

	var resp struct {
		ID      string `json:"id"`
		ChatJID string `json:"chat_jid"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	assert.Equal(t, "MID1", resp.ID)
}

func TestSendMediaMissingFile(t *testing.T) {
	f := &fakeMediaSvc{}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		nil,
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaBadKind(t *testing.T) {
	f := &fakeMediaSvc{sendErr: service.ErrInvalidRequest}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "video"},
		&filePart{field: "file", filename: "v.mp4", contentType: "video/mp4", body: []byte("x")},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaTooLarge(t *testing.T) {
	f := &fakeMediaSvc{}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 32))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		&filePart{field: "file", filename: "big.bin", contentType: "image/jpeg", body: bytes.Repeat([]byte{0x42}, 1024)},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaNotConnected(t *testing.T) {
	f := &fakeMediaSvc{sendErr: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		&filePart{field: "file", filename: "img.jpg", contentType: "image/jpeg", body: []byte("x")},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestGetMediaHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jpg")
	body := []byte("file-bytes")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	f := &fakeMediaSvc{getResp: store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: int64(len(body)), SHA256: "abc", Path: path,
	}}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/MID1")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "image/jpeg", res.Header.Get("Content-Type"))
	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "MID1", f.gotMessageID)
}

func TestGetMediaNotFound(t *testing.T) {
	f := &fakeMediaSvc{getErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/missing")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestGetMediaFileMissing(t *testing.T) {
	f := &fakeMediaSvc{getResp: store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: 100, SHA256: "abc",
		Path: "/tmp/definitely-does-not-exist-87213.bin",
	}}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/MID1")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

var _ = errors.New

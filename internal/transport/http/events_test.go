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
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEventsSvc satisfies service.Service. Most methods return zero values;
// only Status() is exercised by the handler (for the synthetic frame).
type fakeEventsSvc struct {
	status waclient.Status
}

func (f *fakeEventsSvc) Status(context.Context) (waclient.Status, error) { return f.status, nil }
func (f *fakeEventsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeEventsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeEventsSvc) Logout(context.Context) error { return nil }
func (f *fakeEventsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeEventsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeEventsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeEventsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeEventsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeEventsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeEventsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeEventsSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeEventsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeEventsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeEventsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeEventsSvc) DeleteMessage(context.Context, string) error        { return nil }
func (f *fakeEventsSvc) SendReaction(context.Context, string, string) error { return nil }
func (f *fakeEventsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
func (f *fakeEventsSvc) MarkMessageRead(context.Context, string) error    { return nil }
func (f *fakeEventsSvc) SendTyping(context.Context, string, string) error { return nil }
func (f *fakeEventsSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}
func (f *fakeEventsSvc) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeEventsSvc) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f *fakeEventsSvc) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f *fakeEventsSvc) LeaveGroup(context.Context, string) error { return nil }

var _ service.Service = (*fakeEventsSvc)(nil)

func TestEventsHTTPSyntheticConnectionStateFirst(t *testing.T) {
	jid := "me@s.whatsapp.net"
	svc := &fakeEventsSvc{status: waclient.Status{Connected: true, JID: &jid}}
	b := sse.New(8)
	log := newFakeEventsLog()

	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	scanner := bufio.NewScanner(res.Body)
	frame := readSSEFrame(t, scanner)
	assert.Equal(t, "connection.state", frame.event)
	assert.Equal(t, "0", frame.id)
	assert.Contains(t, frame.data, `"connected":true`)
}

func TestEventsHTTPReplay(t *testing.T) {
	log := newFakeEventsLog()
	log.seed("message.received", `{"v":1,"id":1}`)
	log.seed("message.received", `{"v":1,"id":2}`)
	log.seed("message.received", `{"v":1,"id":3}`)

	svc := &fakeEventsSvc{}
	b := sse.New(8)
	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Last-Event-ID", "1")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	scanner := bufio.NewScanner(res.Body)
	// skip synthetic connection.state frame
	_ = readSSEFrame(t, scanner)
	// expect rows 2 and 3
	got2 := readSSEFrame(t, scanner)
	got3 := readSSEFrame(t, scanner)
	assert.Equal(t, "2", got2.id)
	assert.Equal(t, "3", got3.id)
}

func TestEventsHTTPLiveTail(t *testing.T) {
	log := newFakeEventsLog()
	svc := &fakeEventsSvc{}
	b := sse.New(8)
	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()

	scanner := bufio.NewScanner(res.Body)
	_ = readSSEFrame(t, scanner) // synthetic

	// Publish from the test goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish(sse.Event{Seq: 1, Kind: "message.received", Payload: []byte(`{"v":1}`)})
	}()

	frame := readSSEFrame(t, scanner)
	assert.Equal(t, "message.received", frame.event)
	assert.Equal(t, "1", frame.id)
}

func TestEventsHTTPLaggedSubscriber(t *testing.T) {
	log := newFakeEventsLog()
	svc := &fakeEventsSvc{}
	b := sse.New(2) // tiny buffer
	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()

	scanner := bufio.NewScanner(res.Body)
	_ = readSSEFrame(t, scanner) // synthetic

	// Flood — handler can't keep up because we don't read fast enough.
	for i := 0; i < 20; i++ {
		b.Publish(sse.Event{Seq: int64(i + 1), Kind: "x", Payload: []byte(`{"v":1}`)})
	}

	// Drain until we see the terminal error frame.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame := readSSEFrame(t, scanner)
		if frame.event == "error" {
			assert.Contains(t, frame.data, `"events.lagged"`)
			return
		}
	}
	t.Fatal("did not receive terminal error frame")
}

func TestEventsHTTPBadSinceParam(t *testing.T) {
	log := newFakeEventsLog()
	svc := &fakeEventsSvc{}
	b := sse.New(8)
	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?since=abc")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestEventsHTTPBadLastEventIDHeader(t *testing.T) {
	log := newFakeEventsLog()
	svc := &fakeEventsSvc{}
	b := sse.New(8)
	srv := httptest.NewServer(httpapi.EventsHandler(svc, log, b, 25))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Last-Event-ID", "abc")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

// --- test helpers ---

type fakeEventsLog struct {
	rows []store.EventLogEntry
}

func newFakeEventsLog() *fakeEventsLog { return &fakeEventsLog{} }
func (f *fakeEventsLog) seed(kind, payload string) {
	f.rows = append(f.rows, store.EventLogEntry{
		Seq: int64(len(f.rows) + 1), Type: kind, Payload: payload, Time: time.Now(),
	})
}
func (f *fakeEventsLog) Append(_ context.Context, e store.EventLogEntry) (int64, error) {
	e.Seq = int64(len(f.rows) + 1)
	f.rows = append(f.rows, e)
	return e.Seq, nil
}
func (f *fakeEventsLog) SinceSeq(_ context.Context, seq int64, limit int) ([]store.EventLogEntry, error) {
	out := []store.EventLogEntry{}
	for _, r := range f.rows {
		if r.Seq > seq {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type eventsFrame struct{ event, id, data string }

func readSSEFrame(t *testing.T, sc *bufio.Scanner) eventsFrame {
	t.Helper()
	var f eventsFrame
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if f.event != "" || f.data != "" {
				return f
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment (e.g. :ready, :ping)
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			f.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			f.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			f.data = strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatal("EOF before frame complete")
	return f
}

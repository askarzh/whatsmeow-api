package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEventsLog captures Append calls and supplies a controllable seq.
type fakeEventsLog struct {
	appended  []store.EventLogEntry
	nextSeq   int64
	appendErr error
}

func (f *fakeEventsLog) Append(_ context.Context, e store.EventLogEntry) (int64, error) {
	if f.appendErr != nil {
		return 0, f.appendErr
	}
	f.nextSeq++
	e.Seq = f.nextSeq
	f.appended = append(f.appended, e)
	return f.nextSeq, nil
}
func (f *fakeEventsLog) SinceSeq(_ context.Context, seq int64, limit int) ([]store.EventLogEntry, error) {
	out := []store.EventLogEntry{}
	for _, e := range f.appended {
		if e.Seq > seq {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func TestEmitAppendsAndPublishes(t *testing.T) {
	log := &fakeEventsLog{}
	b := sse.New(8)
	_, ch := b.Subscribe()
	em := service.NewEmitter(log, b, slog.Default())

	em.Emit(context.Background(), "message.received", map[string]any{"v": 1, "body": "hi"})

	require.Len(t, log.appended, 1)
	assert.Equal(t, "message.received", log.appended[0].Type)

	select {
	case ev := <-ch:
		assert.Equal(t, int64(1), ev.Seq)
		assert.Equal(t, "message.received", ev.Kind)
		var got map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &got))
		assert.Equal(t, float64(1), got["v"])
		assert.Equal(t, "hi", got["body"])
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestEmitAppendErrorSkipsPublish(t *testing.T) {
	log := &fakeEventsLog{appendErr: errors.New("disk full")}
	b := sse.New(8)
	_, ch := b.Subscribe()
	em := service.NewEmitter(log, b, slog.Default())

	em.Emit(context.Background(), "message.received", map[string]any{"v": 1})

	assert.Empty(t, log.appended)
	select {
	case ev := <-ch:
		t.Fatalf("unexpected publish on append error: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEmitNilBroadcasterIsSafe(t *testing.T) {
	log := &fakeEventsLog{}
	em := service.NewEmitter(log, nil, slog.Default())
	assert.NotPanics(t, func() {
		em.Emit(context.Background(), "x", map[string]any{})
	})
	require.Len(t, log.appended, 1) // append still happens
}

func TestEmitNilEmitterIsSafe(t *testing.T) {
	var em *service.Emitter
	assert.NotPanics(t, func() {
		em.Emit(context.Background(), "x", map[string]any{})
	})
}

// Payload shape goldens: one test per build*Payload helper.

func TestBuildMessageReceivedPayloadText(t *testing.T) {
	in := waclient.IncomingMessage{
		ID:        "WAID1",
		ChatJID:   "alice@s.whatsapp.net",
		SenderJID: "alice@s.whatsapp.net",
		Kind:      "text",
		Body:      "hello",
		Timestamp: time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
		PushName:  "Alice",
	}
	got := service.BuildMessageReceivedPayload(in, store.MediaRef{})
	assertJSONEqual(t, `{
		"v": 1,
		"message_id": "WAID1",
		"chat_jid": "alice@s.whatsapp.net",
		"sender_jid": "alice@s.whatsapp.net",
		"kind": "text",
		"body": "hello",
		"timestamp": "2026-05-08T12:00:00Z",
		"push_name": "Alice"
	}`, got)
}

func TestBuildMessageReceivedPayloadMedia(t *testing.T) {
	in := waclient.IncomingMessage{
		ID: "WAID2", ChatJID: "alice@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Kind: "image", Body: "see attached",
		Timestamp: time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}
	media := store.MediaRef{
		MessageID: "WAID2", MIME: "image/jpeg", Size: 1024,
		SHA256: "abc123",
	}
	got := service.BuildMessageReceivedPayload(in, media)
	assertJSONEqual(t, `{
		"v": 1,
		"message_id": "WAID2",
		"chat_jid": "alice@s.whatsapp.net",
		"sender_jid": "alice@s.whatsapp.net",
		"kind": "image",
		"body": "see attached",
		"timestamp": "2026-05-08T12:00:00Z",
		"media": {
			"ref": "WAID2",
			"mime_type": "image/jpeg",
			"size": 1024,
			"sha256": "abc123",
			"caption": "see attached"
		}
	}`, got)
}

func TestBuildMessageEditedPayload(t *testing.T) {
	got := service.BuildMessageEditedPayload(
		"WAID1", "alice@s.whatsapp.net", "edited body",
		time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	)
	assertJSONEqual(t, `{
		"v": 1,
		"message_id": "WAID1",
		"chat_jid": "alice@s.whatsapp.net",
		"body": "edited body",
		"edited_at": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildMessageDeletedPayload(t *testing.T) {
	got := service.BuildMessageDeletedPayload(
		"WAID1", "alice@s.whatsapp.net",
		time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	)
	assertJSONEqual(t, `{
		"v": 1,
		"message_id": "WAID1",
		"chat_jid": "alice@s.whatsapp.net",
		"deleted_at": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildReactionReceivedPayload(t *testing.T) {
	got := service.BuildReactionReceivedPayload(
		"WAID1", "alice@s.whatsapp.net", "👍",
		time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	)
	assertJSONEqual(t, `{
		"v": 1,
		"target_message_id": "WAID1",
		"sender_jid": "alice@s.whatsapp.net",
		"emoji": "👍",
		"timestamp": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildReceiptReceivedPayload(t *testing.T) {
	in := waclient.IncomingReceipt{
		MessageIDs: []string{"WAID1", "WAID2"},
		ChatJID:    "alice@s.whatsapp.net",
		ReaderJID:  "alice@s.whatsapp.net",
		Type:       "read",
		Timestamp:  time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}
	got := service.BuildReceiptReceivedPayload(in)
	assertJSONEqual(t, `{
		"v": 1,
		"message_ids": ["WAID1", "WAID2"],
		"chat_jid": "alice@s.whatsapp.net",
		"reader_jid": "alice@s.whatsapp.net",
		"type": "read",
		"timestamp": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildTypingReceivedPayload(t *testing.T) {
	got := service.BuildTypingReceivedPayload(
		"alice@s.whatsapp.net", "alice@s.whatsapp.net", "composing",
		time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	)
	assertJSONEqual(t, `{
		"v": 1,
		"chat_jid": "alice@s.whatsapp.net",
		"sender_jid": "alice@s.whatsapp.net",
		"state": "composing",
		"timestamp": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildConnectionStatePayloadConnected(t *testing.T) {
	since := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	jid := "me@s.whatsapp.net"
	got := service.BuildConnectionStatePayload(waclient.Status{
		Connected: true, JID: &jid, Since: &since,
	}, "")
	assertJSONEqual(t, `{
		"v": 1,
		"connected": true,
		"jid": "me@s.whatsapp.net",
		"since": "2026-05-08T12:00:00Z"
	}`, got)
}

func TestBuildConnectionStatePayloadDisconnected(t *testing.T) {
	got := service.BuildConnectionStatePayload(waclient.Status{Connected: false}, "logout")
	assertJSONEqual(t, `{
		"v": 1,
		"connected": false,
		"reason": "logout"
	}`, got)
}

// assertJSONEqual is a small helper that compares two JSON byte slices for
// semantic equality (key order independent).
func assertJSONEqual(t *testing.T, want string, got []byte) {
	t.Helper()
	var w, g any
	require.NoError(t, json.Unmarshal([]byte(want), &w))
	require.NoError(t, json.Unmarshal(got, &g))
	assert.Equal(t, w, g)
}

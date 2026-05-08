# whatsmeow-api Plan 09 — SSE Event Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single `GET /v1/events` Server-Sent-Events endpoint that emits inbound and connection-state events with `Last-Event-ID` resume backed by the existing `events_log` table.

**Architecture:** New `internal/transport/sse` package providing an in-process pub/sub `Broadcaster`. New `internal/service/events.go` providing an `*emitter` that, after each domain-row persist, appends a JSON-payload row to `events_log` and publishes to the broadcaster. New HTTP handler that does (replay-from-`SinceSeq`) → (live-tail-from-broadcaster), with heartbeats and a "lagged" terminal frame on per-subscriber overflow. `service.New` grows one parameter (`*sse.Broadcaster`); existing `service.Service` interface is unchanged because `emit` is private.

**Tech Stack:**
- Go 1.26
- Plan 01–08 stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)
- Existing `store.EventsLog` interface (`Append(ctx, EventLogEntry) (int64, error)`, `SinceSeq(ctx, seq, limit) ([]EventLogEntry, error)`) — no migration

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/transport/sse/broadcaster.go` | NEW | `Event`, `Broadcaster`, `New`, `Subscribe`, `Unsubscribe`, `Publish` |
| `internal/transport/sse/broadcaster_test.go` | NEW | unit tests for the pub/sub primitive |
| `internal/service/events.go` | NEW | `emitter` + per-event `build*Payload` helpers |
| `internal/service/events_test.go` | NEW | golden tests for payload shapes + `emit` contract |
| `internal/service/service.go` | MODIFY | `New` accepts `*sse.Broadcaster`; `*svc` stores `*emitter`; each `handle*` calls `s.emit(...)` |
| `internal/service/service_test.go` | MODIFY | tests cover emit-per-handler; helper for fake broadcaster; updated `service.New` calls |
| `internal/waclient/waclient.go` | MODIFY | add `OnConnectionState(handler func(ConnectionStateEvent))` to interface + new domain type |
| `internal/waclient/whatsmeow_adapter.go` | MODIFY | route whatsmeow `events.Connected` / `events.LoggedOut` / `events.Disconnected` to the registered handler |
| `internal/transport/http/events.go` | NEW | `EventsHandler` (replay + live tail + heartbeat + lagged-error frame) |
| `internal/transport/http/events_test.go` | NEW | HTTP-layer SSE tests with `httptest.NewServer` |
| `internal/transport/http/router.go` | MODIFY | new `Deps.Broadcaster` field; `r.Method(http.MethodGet, "/events", EventsHandler(...))` |
| `cmd/whatsmeow-api/serve.go` | MODIFY | construct one `*sse.Broadcaster`; pass to `service.New` and `http.Deps` |
| `internal/config/config.go` | MODIFY | add `HTTP.SSEHeartbeatSeconds` (default 25) and `HTTP.SSESubscriberBuffer` (default 256) |
| `config.example.toml` | MODIFY | document the two new keys |
| `README.md` | MODIFY | Plan 09 status entry |

No schema migrations.

---

## Task 1: `sse.Broadcaster` pub/sub primitive

**Files:**
- Create: `internal/transport/sse/broadcaster.go`
- Create: `internal/transport/sse/broadcaster_test.go`

**Goal:** A standalone, in-process broadcaster with no service or HTTP dependencies. Single producer, many consumers, drop-on-overflow per subscriber.

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/sse/broadcaster_test.go`:

```go
package sse_test

import (
	"sync"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcasterSingleSubscriber(t *testing.T) {
	b := sse.New(8)
	_, ch := b.Subscribe()

	b.Publish(sse.Event{Seq: 1, Kind: "message.received", Payload: []byte(`{"v":1}`)})

	select {
	case ev := <-ch:
		assert.Equal(t, int64(1), ev.Seq)
		assert.Equal(t, "message.received", ev.Kind)
		assert.JSONEq(t, `{"v":1}`, string(ev.Payload))
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestBroadcasterMultipleSubscribersFanOut(t *testing.T) {
	b := sse.New(8)
	_, ch1 := b.Subscribe()
	_, ch2 := b.Subscribe()

	b.Publish(sse.Event{Seq: 1, Kind: "x", Payload: []byte(`{}`)})

	for _, ch := range []<-chan sse.Event{ch1, ch2} {
		select {
		case ev := <-ch:
			assert.Equal(t, int64(1), ev.Seq)
		case <-time.After(time.Second):
			t.Fatal("subscriber missed event")
		}
	}
}

func TestBroadcasterUnsubscribeStopsDelivery(t *testing.T) {
	b := sse.New(8)
	id, ch := b.Subscribe()
	b.Unsubscribe(id)

	b.Publish(sse.Event{Seq: 1, Kind: "x", Payload: []byte(`{}`)})

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed after Unsubscribe")
	case <-time.After(100 * time.Millisecond):
		// closed channels return immediately; if we hit this branch, Unsubscribe
		// did not close the channel — also acceptable as long as no event was
		// delivered.
	}
}

func TestBroadcasterUnsubscribeIdempotent(t *testing.T) {
	b := sse.New(8)
	id, _ := b.Subscribe()
	b.Unsubscribe(id)
	assert.NotPanics(t, func() { b.Unsubscribe(id) })
}

func TestBroadcasterSlowSubscriberDropped(t *testing.T) {
	b := sse.New(2) // tiny buffer to force overflow
	_, ch := b.Subscribe()

	for i := 0; i < 10; i++ {
		b.Publish(sse.Event{Seq: int64(i + 1), Kind: "x", Payload: []byte(`{}`)})
	}

	// Drain; we expect the channel to close once the buffer overflows.
	closed := false
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
				goto done
			}
		case <-time.After(500 * time.Millisecond):
			goto done
		}
	}
done:
	assert.True(t, closed, "slow subscriber should have its channel closed on overflow")
}

func TestBroadcasterPublishConcurrent(t *testing.T) {
	b := sse.New(1024)
	_, ch := b.Subscribe()

	var wg sync.WaitGroup
	const N = 100
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(sse.Event{Seq: int64(i + 1), Kind: "x", Payload: []byte(`{}`)})
		}(i)
	}
	wg.Wait()

	got := 0
	timeout := time.After(time.Second)
	for got < N {
		select {
		case <-ch:
			got++
		case <-timeout:
			t.Fatalf("only got %d/%d events", got, N)
		}
	}
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
cd /home/askar/src/whatsmeow-api/.worktrees/plan-09-events
go test ./internal/transport/sse/... -run TestBroadcaster
```

Expected: FAIL — package `sse` has no exported symbols yet (the directory exists from Plan 02 but is empty).

- [ ] **Step 3: Implement the broadcaster**

Create `internal/transport/sse/broadcaster.go`:

```go
// Package sse provides an in-process pub/sub primitive used by the SSE event
// stream endpoint.
package sse

import "sync"

// Event is one item emitted on the stream. Seq matches the events_log row's
// Seq field; the SSE id: line uses this value.
type Event struct {
	Seq     int64
	Kind    string
	Payload []byte
}

// Broadcaster fans out Events to a set of subscriber channels. Each
// subscriber gets its own buffered channel; on overflow the broadcaster
// closes that channel (signaling "lagged") and removes it from the map.
type Broadcaster struct {
	mu      sync.RWMutex
	subs    map[uint64]chan Event
	next    uint64
	bufSize int
}

// New constructs a Broadcaster with the given per-subscriber buffer size.
// A buffer size of 0 is treated as 1.
func New(bufSize int) *Broadcaster {
	if bufSize < 1 {
		bufSize = 1
	}
	return &Broadcaster{
		subs:    make(map[uint64]chan Event),
		bufSize: bufSize,
	}
}

// Subscribe registers a new subscriber and returns its id (for Unsubscribe)
// and its receive channel. The channel is closed by the broadcaster on
// Unsubscribe or on overflow.
func (b *Broadcaster) Subscribe() (uint64, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := b.next
	ch := make(chan Event, b.bufSize)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes the subscriber and closes its channel. Idempotent.
func (b *Broadcaster) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Publish fans out an event to all current subscribers. If a subscriber's
// buffer is full it is dropped (channel closed, removed from the map).
func (b *Broadcaster) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			delete(b.subs, id)
			close(ch)
		}
	}
}
```

> Note: `Publish` takes the write lock so a concurrent `Unsubscribe` cannot close a channel mid-send. Trade-off: `Publish` serializes all subscribers; for our scale (a handful of consumers) this is fine. If profiling ever shows contention we can switch to per-subscriber send goroutines.

- [ ] **Step 4: Run tests, verify PASS**

```bash
go test ./internal/transport/sse/... -race -v
```

Expected: 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/sse/broadcaster.go internal/transport/sse/broadcaster_test.go
git commit -m "sse: in-process Broadcaster with per-subscriber drop-on-overflow"
```

---

## Task 2: `service.emitter` + payload builders

**Files:**
- Create: `internal/service/events.go`
- Create: `internal/service/events_test.go`

**Goal:** A library that knows how to translate domain structs into the documented JSON payloads, append to events_log, and publish to a broadcaster. Not yet wired into the `handle*` paths — that's Task 3.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/events_test.go`:

```go
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
	appended []store.EventLogEntry
	nextSeq  int64
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
		MessageID: "WAID2", Mime: "image/jpeg", Size: 1024,
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
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestEmit|TestBuild' -v
```

Expected: FAIL — exported symbols don't exist yet.

- [ ] **Step 3: Implement `events.go`**

Create `internal/service/events.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// Emitter appends to events_log and publishes to the broadcaster after each
// successful append. Construct via NewEmitter; safe to call methods on a nil
// pointer (Emit becomes a no-op).
type Emitter struct {
	log         store.EventsLog
	broadcaster *sse.Broadcaster
	logger      *slog.Logger
}

// NewEmitter constructs an Emitter. The broadcaster may be nil — Emit will
// still append, just won't publish.
func NewEmitter(log store.EventsLog, broadcaster *sse.Broadcaster, logger *slog.Logger) *Emitter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Emitter{log: log, broadcaster: broadcaster, logger: logger}
}

// Emit marshals payload to JSON, appends a row to events_log, and on success
// publishes to the broadcaster. Append failures are logged at WARN and the
// publish is skipped so persistence and broadcast stay aligned.
func (e *Emitter) Emit(ctx context.Context, kind string, payload any) {
	if e == nil || e.log == nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		e.logger.Warn("emit: marshal failed", "kind", kind, "err", err)
		return
	}
	seq, err := e.log.Append(ctx, store.EventLogEntry{
		Type:    kind,
		Payload: string(body),
		Time:    time.Now().UTC(),
	})
	if err != nil {
		e.logger.Warn("emit: append failed", "kind", kind, "err", err)
		return
	}
	if e.broadcaster != nil {
		e.broadcaster.Publish(sse.Event{Seq: seq, Kind: kind, Payload: body})
	}
}

const payloadVersion = 1

// BuildMessageReceivedPayload builds the message.received JSON payload. If
// media is non-empty (MessageID set), a "media" object is included.
func BuildMessageReceivedPayload(m waclient.IncomingMessage, media store.MediaRef) []byte {
	out := map[string]any{
		"v":          payloadVersion,
		"message_id": m.ID,
		"chat_jid":   m.ChatJID,
		"sender_jid": m.SenderJID,
		"kind":       m.Kind,
		"body":       m.Body,
		"timestamp":  m.Timestamp.UTC().Format(time.RFC3339),
	}
	if m.PushName != "" {
		out["push_name"] = m.PushName
	}
	if media.MessageID != "" {
		obj := map[string]any{
			"ref":       media.MessageID,
			"mime_type": media.Mime,
			"size":      media.Size,
			"sha256":    media.SHA256,
		}
		if m.Body != "" {
			obj["caption"] = m.Body
		}
		out["media"] = obj
	}
	body, _ := json.Marshal(out)
	return body
}

func BuildMessageEditedPayload(messageID, chatJID, body string, editedAt time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"v":          payloadVersion,
		"message_id": messageID,
		"chat_jid":   chatJID,
		"body":       body,
		"edited_at":  editedAt.UTC().Format(time.RFC3339),
	})
	return b
}

func BuildMessageDeletedPayload(messageID, chatJID string, deletedAt time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"v":          payloadVersion,
		"message_id": messageID,
		"chat_jid":   chatJID,
		"deleted_at": deletedAt.UTC().Format(time.RFC3339),
	})
	return b
}

func BuildReactionReceivedPayload(targetMsgID, senderJID, emoji string, ts time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"v":                 payloadVersion,
		"target_message_id": targetMsgID,
		"sender_jid":        senderJID,
		"emoji":             emoji,
		"timestamp":         ts.UTC().Format(time.RFC3339),
	})
	return b
}

func BuildReceiptReceivedPayload(r waclient.IncomingReceipt) []byte {
	b, _ := json.Marshal(map[string]any{
		"v":           payloadVersion,
		"message_ids": r.MessageIDs,
		"chat_jid":    r.ChatJID,
		"reader_jid":  r.ReaderJID,
		"type":        r.Type,
		"timestamp":   r.Timestamp.UTC().Format(time.RFC3339),
	})
	return b
}

func BuildTypingReceivedPayload(chatJID, senderJID, state string, ts time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"v":          payloadVersion,
		"chat_jid":   chatJID,
		"sender_jid": senderJID,
		"state":      state,
		"timestamp":  ts.UTC().Format(time.RFC3339),
	})
	return b
}

// BuildConnectionStatePayload builds the connection.state JSON. Reason is
// included only when connected is false.
func BuildConnectionStatePayload(s waclient.Status, reason string) []byte {
	out := map[string]any{
		"v":         payloadVersion,
		"connected": s.Connected,
	}
	if s.JID != nil {
		out["jid"] = *s.JID
	}
	if s.Since != nil {
		out["since"] = s.Since.UTC().Format(time.RFC3339)
	}
	if !s.Connected && reason != "" {
		out["reason"] = reason
	}
	return mustMarshal(out)
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
```

> Note: build helpers ignore marshal errors by design — they only marshal `map[string]any` or simple structs that always succeed. If profiling later shows a bottleneck we can pre-build these via `json.NewEncoder` or sjson; first cut keeps it simple.

- [ ] **Step 4: Run tests, verify PASS**

```bash
go test ./internal/service/... -run 'TestEmit|TestBuild' -race -v
```

Expected: 11 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/events.go internal/service/events_test.go
git commit -m "service: Emitter + per-event payload builders"
```

---

## Task 3: Wire `Emit` into `handle*` paths

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

**Goal:** `service.New` accepts a broadcaster, builds an `*Emitter`, and each `handle*` calls `s.emitter.Emit(...)` after persisting the domain row. Existing tests update their `service.New` call to pass a broadcaster (or nil); new tests assert emission per path.

- [ ] **Step 1: Inventory the existing `handle*` methods**

```bash
grep -n "func (s \*svc) handle" internal/service/service.go
```

You should see at least: `handleIncoming`, `handleEdit`, `handleRevoke` (or `handleDelete`), `handleReaction`, `handleReceipt`, `handleTyping`. If a method exists for one inbound flow but not another, the spec's event types map 1:1 to handlers — either reuse an existing one or split (the simpler path is whichever the implementer finds in the code today).

- [ ] **Step 2: Extend `service.New` signature**

Edit `internal/service/service.go`. Change:

```go
func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, logger *slog.Logger) Service {
```

to:

```go
func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, broadcaster *sse.Broadcaster, logger *slog.Logger) Service {
```

In the function body, build an emitter and store it on `*svc`:

```go
emitter := NewEmitter(bundle.Events, broadcaster, logger)
s := &svc{
    wa:          wa,
    bundle:      bundle,
    mediaStore:  mediaStore,
    logger:      logger,
    emitter:     emitter,
}
```

Add the field to `*svc`:

```go
type svc struct {
    // ... existing fields
    emitter *Emitter
}
```

Add the import: `"github.com/askarzh/whatsmeow-api/internal/transport/sse"`.

- [ ] **Step 3: Add `s.emit(...)` to each `handle*` path**

For each handle* method, after the persist step succeeds, call:

```go
// handleIncoming, after a successful Messages.Put + media metadata persist:
s.emitter.Emit(ctx, "message.received", BuildMessageReceivedPayload(im, mediaRef))

// handleEdit, after EditMessage persisted:
s.emitter.Emit(ctx, "message.edited", BuildMessageEditedPayload(im.ID, im.ChatJID, im.Body, time.Now().UTC()))

// handleRevoke / handleDelete, after the soft-delete:
s.emitter.Emit(ctx, "message.deleted", BuildMessageDeletedPayload(im.RevokeOfID, im.ChatJID, time.Now().UTC()))

// handleReaction, after reaction store update:
s.emitter.Emit(ctx, "reaction.received", BuildReactionReceivedPayload(im.ReactionTargetID, im.SenderJID, im.ReactionEmoji, im.Timestamp))

// handleReceipt, after Receipts.PutBatch:
s.emitter.Emit(ctx, "receipt.received", BuildReceiptReceivedPayload(rec))

// handleTyping (if it exists; otherwise wherever typing is dispatched today):
s.emitter.Emit(ctx, "typing.received", BuildTypingReceivedPayload(...))
```

If the inbound typing event isn't currently handled in service (whatsmeow may send it via a separate event type that's currently filtered out), skip the emit for now and add a `// TODO Plan 09 follow-up` comment — typing is documented in the spec but the scope says "if a handler doesn't exist for an inbound flow, this plan adds the emit on the path that does exist." Coming up empty is acceptable: a `connection.state`-only stream is still useful.

- [ ] **Step 4: Update existing tests' `service.New` calls**

Run:

```bash
grep -n "service.New(" internal/service/service_test.go
```

Update each call site to pass `nil` for the broadcaster argument. Example:

```go
// before
s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
// after
s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil, nil)
```

- [ ] **Step 5: Add per-handler emission tests**

Append to `internal/service/service_test.go`:

```go
// captureEmitter is a test helper that captures every event published
// to a real broadcaster.
func captureEmitter(t *testing.T) (*sse.Broadcaster, <-chan sse.Event) {
	b := sse.New(64)
	_, ch := b.Subscribe()
	t.Cleanup(func() { /* broadcaster has no Close; subscriber goroutine ends with channel close on test cleanup if we wanted, but for fan-out we just rely on GC */ })
	return b, ch
}

func TestHandleIncomingEmitsMessageReceived(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &fakeWA{status: waclient.Status{Connected: true}}
	b, ch := captureEmitter(t)
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), b, nil)

	wa.fireIncoming(waclient.IncomingMessage{
		ID:        "WAID1",
		ChatJID:   "alice@s.whatsapp.net",
		SenderJID: "alice@s.whatsapp.net",
		Kind:      "text",
		Body:      "hi",
		Timestamp: time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	})

	select {
	case ev := <-ch:
		assert.Equal(t, "message.received", ev.Kind)
		assert.Equal(t, int64(1), ev.Seq)
	case <-time.After(time.Second):
		t.Fatal("did not receive message.received event")
	}
}

// Repeat the above pattern for: edit, delete, reaction, receipt.
// Each test fires the appropriate fakeWA hook and asserts the emitted Kind.
```

> The implementer adapts `fakeWA` to expose `fireIncoming`, `fireReceipt`, etc. helpers if those methods don't already exist (Plans 04+ likely added them; check before duplicating).

- [ ] **Step 6: Build and test**

```bash
go build ./...
go test ./... -race
```

Expected: PASS. The HTTP-layer tests still build because `service.Service` interface is unchanged; only `service.New` signature changed and HTTP tests construct fakes, not `*svc`.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: emit per-handler events on inbound message/edit/delete/reaction/receipt"
```

---

## Task 4: `connection.state` events from waclient

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

**Goal:** A new `OnConnectionState` callback on the WAClient interface, fired by the adapter when whatsmeow surfaces `events.Connected`, `events.LoggedOut`, or `events.Disconnected`. Service registers a handler that emits `connection.state`.

- [ ] **Step 1: Add the type and the interface method**

Edit `internal/waclient/waclient.go`. Add near the other inbound types:

```go
// ConnectionStateEvent is emitted on every whatsmeow connection-state
// transition that the daemon cares to surface to consumers.
type ConnectionStateEvent struct {
	Status Status
	Reason string // "logout" | "disconnect" | "login_failed" | "" on connected=true
}
```

Append to the `WAClient` interface:

```go
// Plan 09
OnConnectionState(handler func(ConnectionStateEvent))
```

- [ ] **Step 2: Adapter stub + bridge fakeWA**

In `internal/waclient/whatsmeow_adapter.go`, add a stub:

```go
// OnConnectionState registers a handler invoked on every whatsmeow
// connection-state transition.
func (a *Adapter) OnConnectionState(handler func(ConnectionStateEvent)) {
	a.mu.Lock()
	a.connectionStateHandler = handler
	a.mu.Unlock()
}
```

Add the field to `*Adapter`:

```go
type Adapter struct {
    // ... existing fields
    connectionStateHandler func(ConnectionStateEvent)
}
```

In `internal/service/service_test.go`, add the bridge stub to `fakeWA`:

```go
func (f *fakeWA) OnConnectionState(h func(waclient.ConnectionStateEvent)) {
	f.connectionState = h
}
```

Add the field:

```go
type fakeWA struct {
    // ... existing fields
    connectionState func(waclient.ConnectionStateEvent)
}
```

And a fire helper:

```go
func (f *fakeWA) fireConnectionState(ev waclient.ConnectionStateEvent) {
	if f.connectionState != nil {
		f.connectionState(ev)
	}
}
```

- [ ] **Step 3: Wire the adapter to whatsmeow events**

In `internal/waclient/whatsmeow_adapter.go`, locate the existing whatsmeow event-loop registration (typically `client.AddEventHandler(a.onEvent)` or similar). Inside the dispatch switch, add cases for the connection-state events:

```go
case *events.Connected:
    a.fireConnectionState(ConnectionStateEvent{
        Status: a.statusLocked(), // current snapshot
        Reason: "",
    })
case *events.LoggedOut:
    a.fireConnectionState(ConnectionStateEvent{
        Status: Status{Connected: false},
        Reason: "logout",
    })
case *events.Disconnected:
    a.fireConnectionState(ConnectionStateEvent{
        Status: Status{Connected: false},
        Reason: "disconnect",
    })
```

The exact existing event names may differ; run `go doc go.mau.fi/whatsmeow/types/events | grep -E "^(Connected|LoggedOut|Disconnected)"` to confirm. Adapt if names diverge.

`a.fireConnectionState` is a private helper:

```go
func (a *Adapter) fireConnectionState(ev ConnectionStateEvent) {
	a.mu.Lock()
	h := a.connectionStateHandler
	a.mu.Unlock()
	if h != nil {
		h(ev)
	}
}
```

- [ ] **Step 4: Service registers and emits**

Edit `internal/service/service.go`. In `service.New` (or a follow-up `Init` method called by `New`), register the handler:

```go
wa.OnConnectionState(func(ev waclient.ConnectionStateEvent) {
    s.emitter.Emit(context.Background(), "connection.state",
        json.RawMessage(BuildConnectionStatePayload(ev.Status, ev.Reason)))
})
```

> Note: `Emit` already takes `any` and marshals it; passing `json.RawMessage` round-trips correctly so the payload isn't double-encoded. Alternatively, expose a private `emitRaw(ctx, kind, body []byte)` on `*Emitter` for the same effect.

- [ ] **Step 5: Add the test**

Append to `internal/service/service_test.go`:

```go
func TestConnectionStateEmits(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &fakeWA{}
	b, ch := captureEmitter(t)
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), b, nil)

	jid := "me@s.whatsapp.net"
	wa.fireConnectionState(waclient.ConnectionStateEvent{
		Status: waclient.Status{Connected: true, JID: &jid},
	})

	select {
	case ev := <-ch:
		assert.Equal(t, "connection.state", ev.Kind)
	case <-time.After(time.Second):
		t.Fatal("no connection.state event")
	}
}
```

- [ ] **Step 6: Build and test**

```bash
go build ./...
go test ./... -race
```

Expected: PASS. HTTP fakes don't need updating because only WAClient gained a method, not service.Service.

- [ ] **Step 7: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/whatsmeow_adapter.go internal/service/service.go internal/service/service_test.go
git commit -m "waclient,service: connection.state events"
```

---

## Task 5: HTTP `EventsHandler` (replay + live tail + heartbeat)

**Files:**
- Create: `internal/transport/http/events.go`
- Create: `internal/transport/http/events_test.go`

**Goal:** A handler that resolves the resume cursor, sets SSE headers, emits a synthetic `connection.state` frame, replays from `bundle.Events.SinceSeq`, then live-tails from a broadcaster subscription with periodic heartbeats. On lag, emits one terminal `event: error` frame and returns.

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/events_test.go`:

```go
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
// ... full set of stubs to satisfy service.Service (copy from any existing
// HTTP fake like fakeGroupsSvc; override Status only).

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

type sseFrame struct{ event, id, data string }

func readSSEFrame(t *testing.T, sc *bufio.Scanner) sseFrame {
	t.Helper()
	var f sseFrame
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
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/transport/http/... -run TestEventsHTTP
```

Expected: FAIL — `EventsHandler` undefined.

- [ ] **Step 3: Implement the handler**

Create `internal/transport/http/events.go`:

```go
package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
)

// EventsHandler returns the GET /v1/events SSE handler. heartbeatSeconds is
// the comment-ping interval; pass 0 to disable.
func EventsHandler(svc service.Service, log store.EventsLog, b *sse.Broadcaster, heartbeatSeconds int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolve resume cursor.
		lastSeq, err := resolveLastSeq(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			WriteProblem(w, http.StatusInternalServerError, "internal", "streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ":ready\n\n")
		flusher.Flush()

		// Subscribe BEFORE replay so live events queue while we backfill.
		subID, ch := b.Subscribe()
		defer b.Unsubscribe(subID)

		// Synthetic connection.state at id 0.
		status, _ := svc.Status(r.Context())
		writeFrame(w, "connection.state", "0", service.BuildConnectionStatePayload(status, ""))
		flusher.Flush()

		// Replay.
		const replayBatch = 256
		lastReplayedSeq := lastSeq
		for {
			rows, err := log.SinceSeq(r.Context(), lastReplayedSeq, replayBatch)
			if err != nil {
				return
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				writeFrame(w, row.Type, strconv.FormatInt(row.Seq, 10), []byte(row.Payload))
				lastReplayedSeq = row.Seq
			}
			flusher.Flush()
			if len(rows) < replayBatch {
				break
			}
		}

		// Live tail.
		var heartbeat <-chan time.Time
		if heartbeatSeconds > 0 {
			t := time.NewTicker(time.Duration(heartbeatSeconds) * time.Second)
			defer t.Stop()
			heartbeat = t.C
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat:
				fmt.Fprint(w, ":ping\n\n")
				flusher.Flush()
			case ev, ok := <-ch:
				if !ok {
					writeFrame(w, "error", "",
						[]byte(`{"v":1,"code":"events.lagged","detail":"subscriber buffer overflowed; reconnect with Last-Event-ID"}`))
					flusher.Flush()
					return
				}
				if ev.Seq <= lastReplayedSeq {
					continue
				}
				writeFrame(w, ev.Kind, strconv.FormatInt(ev.Seq, 10), ev.Payload)
				lastReplayedSeq = ev.Seq
				flusher.Flush()
			}
		}
	})
}

// resolveLastSeq parses Last-Event-ID header (preferred) or ?since= query.
// Both must be non-negative integers; missing is OK and returns 0.
func resolveLastSeq(r *http.Request) (int64, error) {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("Last-Event-ID must be a non-negative integer")
		}
		return n, nil
	}
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("since must be a non-negative integer")
		}
		return n, nil
	}
	return 0, nil
}

func writeFrame(w http.ResponseWriter, event, id string, data []byte) {
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// silence unused-import warning if context not used elsewhere
var _ = context.Background
```

- [ ] **Step 4: Run tests, verify PASS**

```bash
go test ./internal/transport/http/... -run TestEventsHTTP -race -v
```

Expected: 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/events.go internal/transport/http/events_test.go
git commit -m "http: GET /v1/events SSE handler with replay + live tail + heartbeat"
```

---

## Task 6: Wire it all together — router, deps, serve, config

**Files:**
- Modify: `internal/transport/http/router.go`
- Modify: `cmd/whatsmeow-api/serve.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.toml`

- [ ] **Step 1: Extend `Deps` and add the route**

Edit `internal/transport/http/router.go`:

```go
type Deps struct {
	Config      config.Config
	Logger      *slog.Logger
	Service     service.Service
	Store       store.Bundle
	Broadcaster *sse.Broadcaster // Plan 09
}
```

Add the import: `"github.com/askarzh/whatsmeow-api/internal/transport/sse"`.

Inside the auth-protected group, after the existing routes:

```go
r.Method(http.MethodGet, "/events",
    EventsHandler(d.Service, d.Store.Events, d.Broadcaster, d.Config.HTTP.SSEHeartbeatSeconds))
```

- [ ] **Step 2: Add config keys**

Edit `internal/config/config.go`. Find the `HTTP` struct and add:

```go
type HTTP struct {
    // ... existing fields
    SSEHeartbeatSeconds int `koanf:"sse_heartbeat_seconds"`
    SSESubscriberBuffer int `koanf:"sse_subscriber_buffer"`
}
```

In the defaults block (or wherever default values are wired):

```go
if c.HTTP.SSEHeartbeatSeconds == 0 {
    c.HTTP.SSEHeartbeatSeconds = 25
}
if c.HTTP.SSESubscriberBuffer == 0 {
    c.HTTP.SSESubscriberBuffer = 256
}
```

Locate the existing default-setting pattern in `config.go` and follow it (some configs use a `Default()` function, others use post-load mutation — match what's there).

- [ ] **Step 3: Update `config.example.toml`**

Append under `[http]`:

```toml
# Plan 09 — SSE event stream
sse_heartbeat_seconds = 25    # comment-ping interval; <=0 disables
sse_subscriber_buffer = 256   # per-subscriber channel buffer; overflow drops the connection
```

- [ ] **Step 4: Wire `cmd/whatsmeow-api/serve.go`**

Find where `service.New(...)` is called (likely in a `runServe` function). Build the broadcaster first:

```go
broadcaster := sse.New(cfg.HTTP.SSESubscriberBuffer)
svc := service.New(wa, bundle, mediaStore, broadcaster, logger)

router := httpapi.NewRouter(httpapi.Deps{
    Config:      cfg,
    Logger:      logger,
    Service:     svc,
    Store:       bundle,
    Broadcaster: broadcaster,
})
```

Add the import: `"github.com/askarzh/whatsmeow-api/internal/transport/sse"`.

- [ ] **Step 5: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. The 11 existing HTTP test fakes do NOT need bridging because `service.Service` interface is unchanged; only `service.New` (a constructor) and `Deps` (an internal struct) gained fields.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/router.go cmd/whatsmeow-api/serve.go internal/config/config.go config.example.toml
git commit -m "cmd,http,config: wire SSE broadcaster and add sse_* config keys"
```

---

## Task 7: End-to-end smoke

**Files:** none modified.

- [ ] **Step 1: Build and start daemon**

```bash
pkill -f "whatsmeow-api serve" 2>/dev/null; sleep 1
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
```

Expected: `app store opened`, `server starting`, no errors.

- [ ] **Step 2: Synthetic frame on connect**

In one terminal:

```bash
curl -N -H "Accept: text/event-stream" http://127.0.0.1:8080/v1/events
```

Expected: `:ready` line, then immediately a `connection.state` frame with `id: 0` and a JSON payload reflecting the daemon's current state. Leave this curl running.

- [ ] **Step 3: Bad input paths**

```bash
# Invalid since=
curl -i -N "http://127.0.0.1:8080/v1/events?since=abc"
# → 400 with code request.invalid

# Invalid Last-Event-ID
curl -i -N -H "Last-Event-ID: abc" "http://127.0.0.1:8080/v1/events"
# → 400 with code request.invalid
```

- [ ] **Step 4: Heartbeat**

Leave a `curl -N` running for ~30 s. Expect a `:ping` line to appear within 25–26 seconds.

- [ ] **Step 5: (Optional) Real round-trip with paired account**

If you have a paired account: send a message from the phone, observe `event: message.received` land on the curl in real time. Then disconnect the curl, send a few more messages, reconnect with `Last-Event-ID: <last seq>`, observe replay catches up.

- [ ] **Step 6: Stop daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -3 /tmp/wmapi.log
```

Expected: `... msg="server stopped"`.

- [ ] **Step 7: No commit**

---

## Task 8: README update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append a Plan 09 entry**

Edit `README.md`. After the Plan 08 line, add:

```markdown
- **Plan 09 (SSE event stream)** shipped: `GET /v1/events` emits a Server-Sent-Events stream of inbound events (`message.received`, `message.edited`, `message.deleted`, `reaction.received`, `receipt.received`, `typing.received`) plus `connection.state` transitions. The stream supports `Last-Event-ID` (or `?since=`) resume backed by the existing `events_log` table, with a synthetic `connection.state` frame at id 0 reflecting the daemon's current state on every reconnect. Per-subscriber buffer (`[http] sse_subscriber_buffer`, default 256) drops slow readers with a terminal `event: error` frame; heartbeat interval configurable via `[http] sse_heartbeat_seconds` (default 25s). Payload contract carries a `"v": 1` field for forward compatibility.
```

Update the trailing line:

```markdown
Outbound message lifecycle events (sent → delivered → read on the wire) and group-lifecycle deltas land in a future plan. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 09"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS
- [ ] Manual smoke (Task 7 Steps 2–4): synthetic frame, 400/400 on bad cursors, heartbeat appears
- [ ] (Optional with paired account) real-time message and replay verified
- [ ] `git log --oneline` shows ~7 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 10 — outbound message lifecycle events** (or whatever the next priority surfaces as).

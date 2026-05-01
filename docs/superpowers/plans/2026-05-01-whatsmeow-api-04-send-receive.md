# whatsmeow-api Plan 04 — Send + Receive + Persist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire whatsmeow's send and receive paths through the service layer into the app store. Ship `POST /v1/messages` for outbound text, plus a service-layer subscription to whatsmeow's incoming message events that persists to chats/messages/contacts.

**Architecture:** waclient stays the WhatsApp adapter. It gains `SendText` (returns a `Sent` envelope with id/timestamp/sender) and `OnIncomingMessage` (registers a callback). The service layer's constructor changes to `service.New(wa, store.Bundle, *slog.Logger)`. On construction, service registers its `handleIncoming` method as the callback so inbound events flow service → store. The existing Plan 02 `Service` interface gains a `SendText` method backed by a new HTTP handler `POST /v1/messages`.

**Tech Stack:**
- Go 1.26
- `go.mau.fi/whatsmeow` (already in go.mod from Plan 02) — `Client.SendMessage`, `events.Message`, `proto/waE2E.Message`, `types.JID`
- `google.golang.org/protobuf/proto.String` (transitively in go.mod)
- All Plan 01/02/03 stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite)

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/waclient/waclient.go` | Modified — add `Sent`, `IncomingMessage` types, `ErrNotConnected` sentinel, `ChatKindFromJID` helper, extend `WAClient` interface with `SendText` and `OnIncomingMessage`. |
| `internal/waclient/waclient_test.go` | Modified — add `TestChatKindFromJID`. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — implement `SendText` against `*whatsmeow.Client`, add `OnIncomingMessage`, extend `onEvent` for `*events.Message`. |
| `internal/service/service.go` | Modified — extend `Service` interface with `SendText`, change `New` signature to `(wa, bundle, logger)`, add `handleIncoming`, register callback in `New`. |
| `internal/service/service_test.go` | Modified — update existing tests for new `New` signature, add `TestSendText*` and `TestHandleIncoming*`. Add an in-memory `store.Bundle` test helper. |
| `internal/transport/http/messages.go` | New — `SendTextHandler(svc)`. |
| `internal/transport/http/messages_test.go` | New — `TestSendText*` tests using a fake Service. |
| `internal/transport/http/router.go` | Modified — register `POST /v1/messages` in the auth-protected group. |
| `cmd/whatsmeow-api/serve.go` | Modified — `service.New(wa, appDB.Bundle(), logger)`. |
| `README.md` | Modified — status section update. |

No files removed. No new dependencies.

---

## Task 1: waclient types, helpers, interface extension, and adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/waclient_test.go`
- Modify: `internal/waclient/whatsmeow_adapter.go` (stubs only — Tasks 2 + 3 fill them in)

This task ships the type contract and a JID helper. The Adapter gets stub implementations of `SendText` and `OnIncomingMessage` so the existing compile-time interface check (`var _ WAClient = (*Adapter)(nil)`) keeps passing. Tasks 2 and 3 replace the stubs with real implementations.

- [ ] **Step 1: Add the failing test for `ChatKindFromJID`**

Edit `internal/waclient/waclient_test.go`. Append:
```go
func TestChatKindFromJID(t *testing.T) {
	cases := []struct {
		jid  string
		want string
	}{
		{"27821234567@s.whatsapp.net", "user"},
		{"123456789-1234567890@g.us", "group"},
		{"status@broadcast", "broadcast"},
		{"chan@newsletter", "newsletter"},
		{"oddball@example.com", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.jid, func(t *testing.T) {
			assert.Equal(t, tc.want, waclient.ChatKindFromJID(tc.jid))
		})
	}
}
```

- [ ] **Step 2: Run the test to confirm failure**

```bash
cd /home/askar/src/whatsmeow-api
go test ./internal/waclient/...
```
Expected: FAIL — `waclient.ChatKindFromJID` undefined.

- [ ] **Step 3: Extend `internal/waclient/waclient.go`**

Add the new types, sentinel, helper, and interface methods. Find the existing sentinel block (`var ErrLoginInProgress = ...`) and insert below it; then extend the `WAClient` interface and add `ChatKindFromJID`.

The full file should look like (replace from `package waclient` through the bottom):
```go
// Package waclient is the only package that imports whatsmeow. It owns the
// *whatsmeow.Client, registers event handlers, and translates whatsmeow types
// into the domain types used by the rest of the daemon.
package waclient

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Status is the daemon's view of the current WhatsApp connection.
type Status struct {
	Connected bool
	JID       *string
	PushName  *string
	Since     *time.Time
}

// QREvent is one frame of the QR-login stream.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// PairEvent is one frame of the phone-pair-login stream.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// Sent is the envelope returned by SendText: enough information for the caller
// to persist the message as our own outbound row.
type Sent struct {
	ID        string
	Timestamp time.Time
	SenderJID string
}

// IncomingMessage is one received message translated out of whatsmeow's
// *events.Message. Plan 04 covers text and media-kind messages; protocol /
// system events (group state changes etc.) are filtered at the adapter and
// never reach the handler.
type IncomingMessage struct {
	ID        string
	ChatJID   string
	ChatKind  string // "user" | "group" | "broadcast" | "newsletter"
	SenderJID string
	Timestamp time.Time
	Kind      string // "text" | "image" | "video" | "audio" | "document" | "sticker"
	Body      string // empty for non-text
	PushName  string
}

// WAClient is the abstraction over whatsmeow used by the rest of the daemon.
type WAClient interface {
	Status() Status
	Resume(ctx context.Context) error
	LoginQR(ctx context.Context) (<-chan QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
	Logout(ctx context.Context) error
	Close() error

	// Plan 04 additions
	SendText(ctx context.Context, chatJID, text string) (Sent, error)
	OnIncomingMessage(handler func(IncomingMessage))
}

// Sentinel errors so callers can distinguish failure modes without parsing strings.
var (
	ErrLoginInProgress = errors.New("waclient: login already in progress")
	ErrAlreadyLoggedIn = errors.New("waclient: already logged in")
	ErrNotLoggedIn     = errors.New("waclient: not logged in")
	ErrNotConnected    = errors.New("waclient: not connected")
)

var phoneRE = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// IsValidPhoneNumber checks that s looks like an E.164 number.
func IsValidPhoneNumber(s string) bool {
	return phoneRE.MatchString(s)
}

// ChatKindFromJID classifies a WhatsApp JID by its server suffix.
func ChatKindFromJID(jid string) string {
	switch {
	case strings.HasSuffix(jid, "@s.whatsapp.net"):
		return "user"
	case strings.HasSuffix(jid, "@g.us"):
		return "group"
	case strings.HasSuffix(jid, "@broadcast"):
		return "broadcast"
	case strings.HasSuffix(jid, "@newsletter"):
		return "newsletter"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 4: Add adapter stubs for the two new interface methods**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the bottom of the file (just before `// compile-time interface check`). Insert:
```go
// SendText is implemented in Task 2.
func (a *Adapter) SendText(ctx context.Context, chatJID, text string) (Sent, error) {
	return Sent{}, errors.New("waclient: SendText not yet implemented")
}

// OnIncomingMessage is implemented in Task 3.
func (a *Adapter) OnIncomingMessage(handler func(IncomingMessage)) {
	// no-op stub; Task 3 wires this into onEvent.
	_ = handler
}
```

`errors` is already imported by the file; no new imports needed.

- [ ] **Step 5: Build and run all tests**

```bash
go build ./...
go vet ./...
go test ./internal/waclient/... -v
```

Expected: PASS — `TestChatKindFromJID` runs, plus `TestValidatePhoneNumber` + `TestErrorsExist` from Plan 02.

`go test ./...` should also be clean since service tests still compile against the unchanged `WAClient` interface (the new methods are added, but service still doesn't call them).

- [ ] **Step 6: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/waclient_test.go internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: types + ChatKindFromJID + interface extension (stubs)"
```

---

## Task 2: Adapter SendText implementation

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test — `SendText` requires a real WhatsApp connection. Coverage comes from the manual smoke (Task 9) and from service-level tests using a fake WAClient.

- [ ] **Step 1: Inspect the whatsmeow API**

```bash
go doc go.mau.fi/whatsmeow.Client.SendMessage
go doc go.mau.fi/whatsmeow.SendResponse
go doc go.mau.fi/whatsmeow/proto/waE2E.Message
go doc go.mau.fi/whatsmeow/types.ParseJID
```

Confirm:
- `Client.SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...SendRequestExtra) (SendResponse, error)`
- `SendResponse{ID string, Timestamp time.Time, ...}`
- `*waE2E.Message{Conversation *string, ImageMessage *ImageMessage, ...}` — for plain text we set `Conversation`.
- `types.ParseJID(string) (types.JID, error)` returns the parsed JID and an error on bad input.

If signatures differ from the above, adapt — the intent is "parse JID, build a text-only Message, call SendMessage, return ID + Timestamp + our own JID".

- [ ] **Step 2: Replace the SendText stub**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the import block and add (alphabetized into the third-party group):
```go
"go.mau.fi/whatsmeow/proto/waE2E"
"go.mau.fi/whatsmeow/types"
"google.golang.org/protobuf/proto"
```

Find the `SendText` stub from Task 1 and replace its body:
```go
// SendText sends a plain-text message to chatJID.
func (a *Adapter) SendText(ctx context.Context, chatJID, text string) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}
	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}
	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send message: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}
```

`fmt` is already imported by the file.

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```

Expected: clean. If you get whatsmeow API mismatches, run `go doc` on the offending symbol and adapt — the intent is documented above.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: PASS. No new automated test for `SendText`; the var-pinned interface check (`var _ WAClient = (*Adapter)(nil)`) at the bottom of the file confirms the method satisfies the interface.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: implement SendText against whatsmeow.Client.SendMessage"
```

---

## Task 3: Adapter OnIncomingMessage + events.Message handling

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

Like Task 2, no automated test for the adapter; manual smoke (Task 9) and service-level tests with a fake cover this.

- [ ] **Step 1: Inspect whatsmeow's events.Message**

```bash
go doc go.mau.fi/whatsmeow/types/events.Message
go doc go.mau.fi/whatsmeow/types.MessageInfo
go doc go.mau.fi/whatsmeow/proto/waE2E.Message
```

Confirm:
- `events.Message{Info types.MessageInfo, Message *waE2E.Message, ...}`
- `MessageInfo{ID string, Chat types.JID, Sender types.JID, Timestamp time.Time, PushName string, ...}`
- The `*waE2E.Message` carries one of: `Conversation` (text), `ExtendedTextMessage` (text with formatting), `ImageMessage`, `VideoMessage`, `AudioMessage`, `DocumentMessage`, `StickerMessage`, plus protocol/system variants we filter out.

If the proto-message accessor names differ, run `go doc` on the variant and adapt.

- [ ] **Step 2: Add the incomingHandler field to Adapter**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the `type Adapter struct` block and add a new field at the bottom (with the other mutex-guarded state):
```go
type Adapter struct {
	container *sqlstore.Container
	logger    *slog.Logger

	mu              sync.Mutex
	client          *whatsmeow.Client
	loginInProgress bool
	lastConnectedAt time.Time

	pairCh           chan string
	incomingHandler  func(IncomingMessage) // Plan 04
}
```

- [ ] **Step 3: Replace the OnIncomingMessage stub**

```go
// OnIncomingMessage registers a handler invoked once per incoming message
// event, after translation into the domain type IncomingMessage. Setting nil
// clears the handler. Calling this twice replaces the previous handler.
func (a *Adapter) OnIncomingMessage(handler func(IncomingMessage)) {
	a.mu.Lock()
	a.incomingHandler = handler
	a.mu.Unlock()
}
```

- [ ] **Step 4: Extend onEvent to dispatch incoming messages**

Find the existing `onEvent` switch and add a `*events.Message` case at the end (just before the closing brace):
```go
	case *events.Message:
		incoming, ok := translateIncoming(evt)
		if !ok {
			return // protocol/system message; skip
		}
		a.mu.Lock()
		h := a.incomingHandler
		a.mu.Unlock()
		if h != nil {
			h(incoming)
		}
	}
```

Then below `signalPair`, add the translation helper:
```go
// translateIncoming converts a whatsmeow events.Message into our domain type.
// Returns (_, false) for protocol/system events that have no text or media body.
func translateIncoming(evt *events.Message) (IncomingMessage, bool) {
	kind, body, hasBody := messageKindAndBody(evt.Message)
	if !hasBody {
		return IncomingMessage{}, false
	}
	return IncomingMessage{
		ID:        evt.Info.ID,
		ChatJID:   evt.Info.Chat.String(),
		ChatKind:  ChatKindFromJID(evt.Info.Chat.String()),
		SenderJID: evt.Info.Sender.String(),
		Timestamp: evt.Info.Timestamp,
		Kind:      kind,
		Body:      body,
		PushName:  evt.Info.PushName,
	}, true
}

// messageKindAndBody picks the relevant field out of a *waE2E.Message and
// returns ("", "", false) for variants we don't persist (protocol/system, etc.).
func messageKindAndBody(m *waE2E.Message) (string, string, bool) {
	switch {
	case m == nil:
		return "", "", false
	case m.Conversation != nil:
		return "text", *m.Conversation, true
	case m.ExtendedTextMessage != nil && m.ExtendedTextMessage.Text != nil:
		return "text", *m.ExtendedTextMessage.Text, true
	case m.ImageMessage != nil:
		return "image", "", true
	case m.VideoMessage != nil:
		return "video", "", true
	case m.AudioMessage != nil:
		return "audio", "", true
	case m.DocumentMessage != nil:
		return "document", "", true
	case m.StickerMessage != nil:
		return "sticker", "", true
	default:
		return "", "", false
	}
}
```

- [ ] **Step 5: Build and run all tests**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: clean. Adapter compiles; no new tests at this layer.

- [ ] **Step 6: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: OnIncomingMessage + events.Message dispatch"
```

---

## Task 4: Update existing service tests for the new New signature

**Files:**
- Modify: `internal/service/service.go` (signature change)
- Modify: `internal/service/service_test.go` (update existing tests)
- Modify: `cmd/whatsmeow-api/serve.go` (update one call site so the build stays green)

This task changes the constructor signature WITHOUT adding the new behavior. Tasks 5–6 implement `SendText` and `handleIncoming`. Splitting it this way keeps each commit small and reviewable.

- [ ] **Step 1: Update the Service interface and constructor**

Edit `internal/service/service.go`. Replace the file content:
```go
// Package service holds the daemon's use cases. Plan 02 shipped pass-through
// methods over WAClient; Plan 04 adds SendText + inbound persistence over the
// app store.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error
}

type svc struct {
	wa     waclient.WAClient
	bundle store.Bundle
	logger *slog.Logger
}

// New constructs a Service backed by the given WAClient and store bundle.
func New(wa waclient.WAClient, bundle store.Bundle, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &svc{wa: wa, bundle: bundle, logger: logger}
}

func (s *svc) Status(_ context.Context) (waclient.Status, error) {
	return s.wa.Status(), nil
}

func (s *svc) LoginQR(ctx context.Context) (<-chan waclient.QREvent, error) {
	return s.wa.LoginQR(ctx)
}

func (s *svc) LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error) {
	if !waclient.IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}
	return s.wa.LoginPhone(ctx, phoneNumber)
}

func (s *svc) Logout(ctx context.Context) error {
	return s.wa.Logout(ctx)
}
```

The `SendText` method on `Service` will be added in Task 5.

- [ ] **Step 2: Update the existing service_test.go**

Edit `internal/service/service_test.go`. The existing `fakeWA` doesn't implement the new `SendText` and `OnIncomingMessage` methods. Add them at the bottom of the existing fakeWA methods (before `func TestStatusPassThrough`):

```go
func (f *fakeWA) SendText(context.Context, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) OnIncomingMessage(func(waclient.IncomingMessage)) {}
```

Then update every `service.New(f)` call to `service.New(f, store.Bundle{}, nil)`. There are six call sites (TestStatusPassThrough, TestLoginQRPassThrough, TestLoginQRError, TestLoginPhoneRejectsBadNumber, TestLoginPhonePassThrough, TestLogoutPassThrough).

Add `"github.com/askarzh/whatsmeow-api/internal/store"` to the import block.

- [ ] **Step 3: Update cmd/whatsmeow-api/serve.go**

Edit `cmd/whatsmeow-api/serve.go`. Find the existing call:
```go
svc := service.New(wa)
```

Replace with:
```go
svc := service.New(wa, appDB.Bundle(), logger)
```

(The `httpapi.NewServer` call below already passes `Service: svc`. The `Store: appDB.Bundle()` field stays — it's still threaded into Deps for future plans that want a direct handle.)

- [ ] **Step 4: Build, vet, run all tests**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: PASS. The signature change ripples cleanly; no behavior changes yet.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go cmd/whatsmeow-api/serve.go
git commit -m "service: New takes Bundle + logger"
```

---

## Task 5: Service.SendText implementation

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go` (add fake bundle helper + tests)

- [ ] **Step 1: Add the in-memory Bundle helper to service_test.go**

Edit `internal/service/service_test.go`. Add this block above the existing `fakeWA` definition (after the imports):

```go
// inMemoryBundle returns a store.Bundle whose interfaces are backed by simple
// in-memory maps. Sufficient for service-level tests; not thread-safe.
type memChats map[string]store.Chat
type memMessages map[string]store.Message
type memContacts map[string]store.Contact

func newInMemoryBundle() (store.Bundle, *memChats, *memMessages, *memContacts) {
	c := memChats{}
	m := memMessages{}
	co := memContacts{}
	return store.Bundle{
		Chats:    &chatStore{m: c},
		Messages: &messageStore{m: m},
		Contacts: &contactStore{m: co},
		Media:    &mediaStore{},
		Events:   &eventsStore{},
		KV:       &kvStore{m: map[string]string{}},
	}, &c, &m, &co
}

type chatStore struct{ m memChats }

func (s *chatStore) Put(_ context.Context, c store.Chat) error { s.m[c.JID] = c; return nil }
func (s *chatStore) Get(_ context.Context, jid string) (store.Chat, error) {
	c, ok := s.m[jid]
	if !ok {
		return store.Chat{}, store.ErrNotFound
	}
	return c, nil
}
func (s *chatStore) List(context.Context, bool) ([]store.Chat, error) { return nil, nil }
func (s *chatStore) SetArchived(context.Context, string, bool) error  { return nil }

type messageStore struct{ m memMessages }

func (s *messageStore) Put(_ context.Context, msg store.Message) error { s.m[msg.ID] = msg; return nil }
func (s *messageStore) Get(_ context.Context, id string) (store.Message, error) {
	msg, ok := s.m[id]
	if !ok {
		return store.Message{}, store.ErrNotFound
	}
	return msg, nil
}
func (s *messageStore) ListByChat(context.Context, string, int, time.Time) ([]store.Message, error) {
	return nil, nil
}
func (s *messageStore) Search(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (s *messageStore) SoftDelete(context.Context, string, time.Time) error { return nil }

type contactStore struct{ m memContacts }

func (s *contactStore) Put(_ context.Context, c store.Contact) error { s.m[c.JID] = c; return nil }
func (s *contactStore) Get(_ context.Context, jid string) (store.Contact, error) {
	c, ok := s.m[jid]
	if !ok {
		return store.Contact{}, store.ErrNotFound
	}
	return c, nil
}
func (s *contactStore) List(context.Context) ([]store.Contact, error) { return nil, nil }

type mediaStore struct{}

func (s *mediaStore) Put(context.Context, store.MediaRef) error                          { return nil }
func (s *mediaStore) GetByMessageID(context.Context, string) (store.MediaRef, error)     { return store.MediaRef{}, store.ErrNotFound }

type eventsStore struct{}

func (s *eventsStore) Append(context.Context, store.EventLogEntry) (int64, error)         { return 0, nil }
func (s *eventsStore) SinceSeq(context.Context, int64, int) ([]store.EventLogEntry, error) { return nil, nil }

type kvStore struct{ m map[string]string }

func (s *kvStore) Get(_ context.Context, k string) (string, error) {
	v, ok := s.m[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}
func (s *kvStore) Set(_ context.Context, k, v string) error { s.m[k] = v; return nil }
func (s *kvStore) Delete(_ context.Context, k string) error { delete(s.m, k); return nil }
```

- [ ] **Step 2: Add the failing SendText tests**

Append to `internal/service/service_test.go`:

```go
type sendableFakeWA struct {
	fakeWA
	sentArgs   [3]string // chat, text, sender (sender filled by SendText)
	sendResp   waclient.Sent
	sendErr    error
	calledSend bool
}

func (f *sendableFakeWA) SendText(_ context.Context, chatJID, text string) (waclient.Sent, error) {
	f.calledSend = true
	f.sentArgs[0] = chatJID
	f.sentArgs[1] = text
	return f.sendResp, f.sendErr
}

func TestSendTextSuccess(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _ := newInMemoryBundle()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, nil)

	got, err := s.SendText(ctx, "27821234567@s.whatsapp.net", "hello")
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "hello", got.Body)
	assert.Equal(t, "text", got.Kind)
	assert.Equal(t, "me@s.whatsapp.net", got.SenderJID)
	assert.True(t, got.Timestamp.Equal(now))

	// Persistence side effects:
	require.Contains(t, *msgs, "MID1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
	chat := (*chats)["27821234567@s.whatsapp.net"]
	assert.True(t, chat.LastMsgAt.Equal(now))
	assert.Equal(t, "user", chat.Kind)
}

func TestSendTextValidation(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	cases := []struct{ chat, text, expect string }{
		{"", "hello", "chat_jid"},
		{"a@s.whatsapp.net", "", "text"},
		{"a@s.whatsapp.net", strings.Repeat("x", 4097), "text"},
	}
	for _, tc := range cases {
		t.Run(tc.expect, func(t *testing.T) {
			_, err := s.SendText(ctx, tc.chat, tc.text)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
			assert.False(t, wa.calledSend, "fake WA must not be called on validation failure")
		})
	}
}

func TestSendTextNotConnected(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{sendErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, nil)
	_, err := s.SendText(ctx, "a@s.whatsapp.net", "hi")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestSendTextPersistFailureStillSucceeds(t *testing.T) {
	// Use a bundle whose Messages.Put errors but Chats.Put works.
	failMsgs := &failingMessageStore{}
	bundle := store.Bundle{
		Chats:    &chatStore{m: memChats{}},
		Messages: failMsgs,
		Contacts: &contactStore{m: memContacts{}},
		Media:    &mediaStore{},
		Events:   &eventsStore{},
		KV:       &kvStore{m: map[string]string{}},
	}
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID2", Timestamp: time.Now(), SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, nil)

	got, err := s.SendText(context.Background(), "a@s.whatsapp.net", "hello")
	require.NoError(t, err) // persistence failure is logged, not returned
	assert.Equal(t, "MID2", got.ID)
	assert.True(t, failMsgs.called)
}

type failingMessageStore struct {
	called bool
}

func (f *failingMessageStore) Put(context.Context, store.Message) error {
	f.called = true
	return errors.New("boom")
}
func (f *failingMessageStore) Get(context.Context, string) (store.Message, error) {
	return store.Message{}, store.ErrNotFound
}
func (f *failingMessageStore) ListByChat(context.Context, string, int, time.Time) ([]store.Message, error) {
	return nil, nil
}
func (f *failingMessageStore) Search(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *failingMessageStore) SoftDelete(context.Context, string, time.Time) error { return nil }
```

Add `"strings"` and `"errors"` to the imports if not already present.

- [ ] **Step 3: Confirm tests fail**

```bash
go test ./internal/service/... -run TestSendText
```

Expected: FAIL — `service.ErrInvalidRequest` and `(*svc).SendText` undefined.

- [ ] **Step 4: Implement service.SendText**

Edit `internal/service/service.go`. Add to the imports:
```go
"errors"
"strings"
"time"
```

Add the sentinel near `ErrInvalidRequest`:
```go
var ErrInvalidRequest = errors.New("service: invalid request")
```

Add `SendText` to the interface:
```go
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error

	SendText(ctx context.Context, chatJID, text string) (store.Message, error)
}
```

Add the method implementation at the bottom of the file:
```go
const maxTextLen = 4096

func (s *svc) SendText(ctx context.Context, chatJID, text string) (store.Message, error) {
	if strings.TrimSpace(chatJID) == "" {
		return store.Message{}, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if text == "" {
		return store.Message{}, fmt.Errorf("%w: text is required", ErrInvalidRequest)
	}
	if len(text) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	sent, err := s.wa.SendText(ctx, chatJID, text)
	if err != nil {
		return store.Message{}, err
	}

	msg := store.Message{
		ID:        sent.ID,
		ChatJID:   chatJID,
		SenderJID: sent.SenderJID,
		Timestamp: sent.Timestamp,
		Kind:      "text",
		Body:      text,
	}
	if err := s.bundle.Messages.Put(ctx, msg); err != nil {
		s.logger.Warn("persist outbound message failed; whatsmeow echo will heal", "id", sent.ID, "err", err)
	}
	if err := s.bundle.Chats.Put(ctx, store.Chat{
		JID:       chatJID,
		Kind:      waclient.ChatKindFromJID(chatJID),
		LastMsgAt: sent.Timestamp,
	}); err != nil {
		s.logger.Warn("upsert chat on send failed", "chat_jid", chatJID, "err", err)
	}
	return msg, nil
}

// Imports used by handleIncoming (Task 6); silence "unused" until Task 6.
var _ = time.Time{}
```

Remove the `_ = time.Time{}` once Task 6 lands.

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/service/... -v
```

Expected: all PASS, including the 4 new SendText tests.

- [ ] **Step 6: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: SendText with validation + persistence"
```

---

## Task 6: Service.handleIncoming + OnIncomingMessage registration

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Extend the fake WAClient to record the registered handler**

Edit `internal/service/service_test.go`. Replace the existing `OnIncomingMessage` stub on `fakeWA` with one that captures the handler:

Find on `fakeWA`:
```go
func (f *fakeWA) OnIncomingMessage(func(waclient.IncomingMessage)) {}
```

Replace with:
```go
func (f *fakeWA) OnIncomingMessage(h func(waclient.IncomingMessage)) {
	f.incoming = h
}
```

Add the field to the struct (find `type fakeWA struct {` and append):
```go
	incoming func(waclient.IncomingMessage)
```

The `sendableFakeWA` embeds `fakeWA` so the field is inherited.

- [ ] **Step 2: Add the failing handleIncoming tests**

Append to `internal/service/service_test.go`:
```go
func TestHandleIncomingNewChat(t *testing.T) {
	bundle, chats, msgs, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	_ = service.New(wa, bundle, nil) // registers s.handleIncoming with wa

	require.NotNil(t, wa.incoming, "service.New must register an incoming handler")

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN1",
		ChatJID:   "27821234567@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "27821234567@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "text",
		Body:      "hi from phone",
		PushName:  "Alice",
	})

	require.Contains(t, *msgs, "MIN1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
	chat := (*chats)["27821234567@s.whatsapp.net"]
	assert.Equal(t, 1, chat.UnreadCount)
	assert.Equal(t, "user", chat.Kind)

	require.Contains(t, *contacts, "27821234567@s.whatsapp.net")
	assert.Equal(t, "Alice", (*contacts)["27821234567@s.whatsapp.net"].PushName)
}

func TestHandleIncomingExistingChat(t *testing.T) {
	bundle, chats, _, _ := newInMemoryBundle()
	(*chats)["chat@s.whatsapp.net"] = store.Chat{
		JID: "chat@s.whatsapp.net", Kind: "user", UnreadCount: 3,
	}
	wa := &sendableFakeWA{}
	service.New(wa, bundle, nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN2",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(2000, 0).UTC(),
		Kind:      "text",
		Body:      "another",
		PushName:  "B",
	})

	chat := (*chats)["chat@s.whatsapp.net"]
	assert.Equal(t, 4, chat.UnreadCount)
}

func TestHandleIncomingNonText(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	service.New(wa, bundle, nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN3",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(3000, 0).UTC(),
		Kind:      "image",
		Body:      "",
		PushName:  "C",
	})

	require.Contains(t, *msgs, "MIN3")
	got := (*msgs)["MIN3"]
	assert.Equal(t, "image", got.Kind)
	assert.Empty(t, got.Body)
}

func TestHandleIncomingEmptyPushName(t *testing.T) {
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	service.New(wa, bundle, nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN4",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "sender@s.whatsapp.net",
		Timestamp: time.Unix(4000, 0).UTC(),
		Kind:      "text",
		Body:      "yo",
		PushName:  "",
	})

	// No contact upsert when push_name is empty.
	assert.NotContains(t, *contacts, "sender@s.whatsapp.net")
}
```

- [ ] **Step 3: Confirm tests fail**

```bash
go test ./internal/service/... -run TestHandleIncoming
```

Expected: FAIL — handler isn't registered yet (`wa.incoming` is nil).

- [ ] **Step 4: Implement handleIncoming + register in New**

Edit `internal/service/service.go`. Update `New` to register the callback:
```go
func New(wa waclient.WAClient, bundle store.Bundle, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &svc{wa: wa, bundle: bundle, logger: logger}
	wa.OnIncomingMessage(s.handleIncoming)
	return s
}
```

Add the method at the bottom (and remove the `var _ = time.Time{}` placeholder from Task 5):
```go
func (s *svc) handleIncoming(msg waclient.IncomingMessage) {
	ctx := context.Background()

	if msg.PushName != "" {
		if err := s.bundle.Contacts.Put(ctx, store.Contact{
			JID:      msg.SenderJID,
			PushName: msg.PushName,
		}); err != nil {
			s.logger.Warn("upsert contact on incoming failed", "jid", msg.SenderJID, "err", err)
		}
	}

	chat, err := s.bundle.Chats.Get(ctx, msg.ChatJID)
	if err != nil {
		// Treat any error (including ErrNotFound) as "no existing chat".
		chat = store.Chat{JID: msg.ChatJID, Kind: msg.ChatKind}
	}
	chat.LastMsgAt = msg.Timestamp
	chat.UnreadCount++
	if chat.Kind == "" {
		chat.Kind = msg.ChatKind
	}
	if err := s.bundle.Chats.Put(ctx, chat); err != nil {
		s.logger.Warn("upsert chat on incoming failed", "jid", msg.ChatJID, "err", err)
	}

	if err := s.bundle.Messages.Put(ctx, store.Message{
		ID:        msg.ID,
		ChatJID:   msg.ChatJID,
		SenderJID: msg.SenderJID,
		Timestamp: msg.Timestamp,
		Kind:      msg.Kind,
		Body:      msg.Body,
	}); err != nil {
		s.logger.Warn("persist incoming message failed", "id", msg.ID, "err", err)
	}
}
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/service/... -v
```

Expected: all PASS — Plan 02's tests still green, Task 5's SendText tests still green, the 4 new handleIncoming tests now green.

- [ ] **Step 6: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: handleIncoming + OnIncomingMessage registration"
```

---

## Task 7: HTTP handler — POST /v1/messages

**Files:**
- Create: `internal/transport/http/messages.go`
- Create: `internal/transport/http/messages_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/messages_test.go`:
```go
package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat string
	gotText string
}

func (f *fakeSendSvc) Status(context.Context) (waclient.Status, error) { return waclient.Status{}, nil }
func (f *fakeSendSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeSendSvc) Logout(context.Context) error { return nil }
func (f *fakeSendSvc) SendText(_ context.Context, chat, text string) (store.Message, error) {
	f.gotChat = chat
	f.gotText = text
	return f.resp, f.err
}

var _ service.Service = (*fakeSendSvc)(nil)

func TestSendTextHappyPath(t *testing.T) {
	ts := time.Date(2026, 5, 1, 12, 34, 56, 0, time.UTC)
	f := &fakeSendSvc{resp: store.Message{
		ID: "MID1", ChatJID: "27821234567@s.whatsapp.net", Timestamp: ts,
		Kind: "text", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"chat_jid":"27821234567@s.whatsapp.net","text":"hi"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()

	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, "27821234567@s.whatsapp.net", f.gotChat)
	assert.Equal(t, "hi", f.gotText)

	var got struct {
		ID      string    `json:"id"`
		ChatJID string    `json:"chat_jid"`
		Ts      time.Time `json:"ts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "27821234567@s.whatsapp.net", got.ChatJID)
	assert.True(t, got.Ts.Equal(ts))
}

func TestSendTextRejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(`not json`))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
}

func TestSendTextRejectsEmptyText(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(`{"chat_jid":"a@s.whatsapp.net","text":""}`))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextRejectsLongText(t *testing.T) {
	srv := httptest.NewServer(httpapi.SendTextHandler(&fakeSendSvc{}))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"` + strings.Repeat("x", 4097) + `"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextRejectsValidationFromService(t *testing.T) {
	// Service returns ErrInvalidRequest -> 400.
	f := &fakeSendSvc{err: service.ErrInvalidRequest}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTextNotConnected(t *testing.T) {
	f := &fakeSendSvc{err: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestSendTextInternalError(t *testing.T) {
	f := &fakeSendSvc{err: errors.New("boom")}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()
	body := `{"chat_jid":"a@s.whatsapp.net","text":"hi"}`
	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/transport/http/... -run TestSendText
```

Expected: FAIL — `httpapi.SendTextHandler` undefined.

- [ ] **Step 3: Implement the handler**

Create `internal/transport/http/messages.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type sendTextRequest struct {
	ChatJID string `json:"chat_jid"`
	Text    string `json:"text"`
}

const maxTextLen = 4096

// SendTextHandler handles POST /v1/messages: send a text message to a chat.
func SendTextHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sendTextRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		if req.ChatJID == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "chat_jid is required")
			return
		}
		if req.Text == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "text is required")
			return
		}
		if len(req.Text) > maxTextLen {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "text exceeds 4096 bytes")
			return
		}

		msg, err := svc.SendText(r.Context(), req.ChatJID, req.Text)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			case errors.Is(err, waclient.ErrNotConnected):
				WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       msg.ID,
			"chat_jid": msg.ChatJID,
			"ts":       msg.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	})
}
```

The handcrafted ts format matches RFC 3339 with optional sub-second precision and UTC zone — same as Plan 02's status handler.

- [ ] **Step 4: Wire the route in router.go**

Edit `internal/transport/http/router.go`. Find the auth-protected group:
```go
		r.Group(func(r chi.Router) {
			r.Use(RequireBearerToken(d.Config.Auth.Token))
			r.Method(http.MethodGet, "/status", StatusHandler(d.Service))
			r.Method(http.MethodPost, "/login/qr", LoginQRHandler(d.Service))
			r.Method(http.MethodPost, "/login/phone", LoginPhoneHandler(d.Service))
			r.Method(http.MethodPost, "/logout", LogoutHandler(d.Service))
		})
```

Append a line for the new endpoint:
```go
			r.Method(http.MethodPost, "/messages", SendTextHandler(d.Service))
```

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/transport/http/... -v
```

Expected: PASS — all existing tests + 7 new SendText tests.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/messages.go internal/transport/http/messages_test.go internal/transport/http/router.go
git commit -m "http: POST /v1/messages handler with text validation"
```

---

## Task 8: End-to-end smoke test

**Files:** none modified.

This task verifies the daemon's outbound and inbound paths work against a real WhatsApp account. It REQUIRES a paired account from Plan 02's `login qr` flow. If you don't want to test against a real account, perform the partial smoke (steps 1-3) and stop.

- [ ] **Step 1: Build and confirm the new endpoint exists**

```bash
make build
ls -la bin/whatsmeow-api
```

- [ ] **Step 2: Start the daemon (assumes a paired session in `data/`)**

```bash
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
./bin/whatsmeow-api status
```

Expected: status shows "connected as <your jid> (...)". If it shows "not connected", run `./bin/whatsmeow-api login qr` first and scan with WhatsApp.

- [ ] **Step 3: Validation smoke (no real send)**

```bash
# Empty text -> 400
curl -i -X POST -H "Content-Type: application/json" \
  -d '{"chat_jid":"+27821234567@s.whatsapp.net","text":""}' \
  http://127.0.0.1:8080/v1/messages
# Missing chat -> 400
curl -i -X POST -H "Content-Type: application/json" \
  -d '{"text":"hi"}' \
  http://127.0.0.1:8080/v1/messages
```

Expected: both return `HTTP/1.1 400 Bad Request` with `application/problem+json` and the right `code` field.

- [ ] **Step 4: Real send to your own JID**

Replace `<YOUR_JID>` with a real JID (e.g. your own number). You'll see the message arrive on the linked phone.

```bash
JID="<YOUR_JID>"  # e.g. 27821234567@s.whatsapp.net
curl -i -X POST -H "Content-Type: application/json" \
  -d "{\"chat_jid\":\"$JID\",\"text\":\"hello from plan 04 — $(date)\"}" \
  http://127.0.0.1:8080/v1/messages
```

Expected: 201 with `{"id":"3EB05...","chat_jid":"...","ts":"..."}`.

- [ ] **Step 5: Verify outbound row in the DB**

```bash
sqlite3 data/whatsmeow-app.db 'SELECT id, chat_jid, sender_jid, kind, body FROM messages ORDER BY ts DESC LIMIT 3'
```

Expected: the ID from Step 4's response is the first row, with kind=text and body matching what you sent.

- [ ] **Step 6: Reply from the phone to the linked account**

Send any message FROM your phone TO the daemon's account. Wait ~3 seconds.

- [ ] **Step 7: Verify inbound row + chat upsert**

```bash
sqlite3 data/whatsmeow-app.db 'SELECT id, chat_jid, sender_jid, kind, body FROM messages ORDER BY ts DESC LIMIT 5'
sqlite3 data/whatsmeow-app.db 'SELECT jid, name, kind, last_msg_at, unread_count FROM chats'
sqlite3 data/whatsmeow-app.db 'SELECT jid, push_name FROM contacts'
```

Expected:
- A second `messages` row with `sender_jid` = your phone's JID, body containing the reply text.
- The `chats` row for the JID has `unread_count >= 1`.
- The `contacts` row has the phone's push_name.

- [ ] **Step 8: Stop the daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -5 /tmp/wmapi.log
```

Expected last log line: `... msg="server stopped"`.

- [ ] **Step 9: Mark this task done**

No commit — code is unchanged.

If any step fails, fix the underlying code in the appropriate package before proceeding.

---

## Task 9: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status section**

Edit `README.md`. Replace the existing Status block:

```markdown
## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.
- **Plan 03 (app store)** shipped: SQLite-backed persistence layer with seven tables (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) and `golang-migrate`-driven schema migrations that auto-run on `serve`. No handlers read it yet; consumers arrive in Plan 04+.

Messaging endpoints (send / receive / list / search) land in Plan 04+.
```

…with:
```markdown
## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.
- **Plan 03 (app store)** shipped: SQLite-backed persistence layer with seven tables (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) and `golang-migrate`-driven schema migrations that auto-run on `serve`.
- **Plan 04 (send + receive)** shipped: `POST /v1/messages` sends a text message via whatsmeow and persists the outbound row. Inbound message events from whatsmeow are persisted automatically (text + media kinds; media metadata lands in Plan 06). `chats.last_msg_at`, `chats.unread_count`, and `contacts.push_name` update in real time.

Listing / searching the persisted messages lands in Plan 05; reactions / replies / edits / deletes / read receipts in Plan 07.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 04"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` all PASS, including new service + HTTP tests
- [ ] Manual smoke from Task 8 all green (or at minimum: validation paths in Task 8 Step 3 return 400)
- [ ] (Optional, requires a real WhatsApp account) Task 8 Steps 4-7 round-trip — outbound send arrives, inbound reply lands in the DB
- [ ] `git log --oneline` shows ~9 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 05 — list chats / messages / search**.

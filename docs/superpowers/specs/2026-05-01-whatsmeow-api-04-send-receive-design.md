# whatsmeow-api Plan 04 — Send + Receive + Persist Design

**Date:** 2026-05-01
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 03 (app store + SQLite + migrations) — merged.

## 1. Purpose

Wire whatsmeow's send and receive paths through the service layer into the app store. After Plan 04 the daemon can send a text message via `POST /v1/messages` and persists every incoming message event. Plan 03's Bundle, which has been threaded into HTTP handlers but not consumed, finally has a real consumer.

Plan 04 is the data-flow plan. Plans 05+ expose what's persisted (list / search) and Plan 09 emits live events.

## 2. Goals

- One new HTTP endpoint: `POST /v1/messages` (text-only). Synchronous: waits for whatsmeow's ack, persists, returns 201.
- Inbound persistence runs in the service layer, not in waclient. waclient stays the WhatsApp adapter; it translates events into a domain type (`IncomingMessage`) and invokes a callback the service registered at construction.
- Persist all incoming message kinds (text, image, video, audio, document, sticker), not just text. For non-text kinds, `messages.body` is empty and `messages.kind` reflects the type. This keeps `chats.last_msg_at` truthful for chats with media-only history; Plan 06 fills in media metadata.
- `chats` and `contacts` are upserted on every inbound event so Plan 05's chat-list shows the right state without a backfill step.

## 3. Non-goals (Plan 04)

- Listing or reading the persisted messages → Plan 05.
- Replies, edits, deletes, reactions, read receipts, typing → Plan 07.
- Media handling (download, upload, store on disk, mime detection) → Plan 06.
- Group create / membership management → Plan 08.
- Realtime delivery to API clients (SSE) → Plan 09. `events_log` stays untouched in this plan.
- Backpressure / worker pool for inbound. Whatsmeow's event goroutine drives `handleIncoming` synchronously; this is fine for a single-account daemon and Plan 09 will revisit.

## 4. Architecture

```
HTTP POST /v1/messages
        │
        ▼
service.SendText(ctx, chatJID, text)
        │
        ├── waclient.SendText(...)  → whatsmeow.Client.SendMessage  → WhatsApp servers
        │       returns Sent{ID, Timestamp, SenderJID}
        │
        └── store.Messages.Put + store.Chats.Put (last_msg_at)
                returns store.Message → 201 JSON

whatsmeow events.Message (whatsmeow's event goroutine)
        │
        ▼
adapter.onEvent → adapter.incomingHandler(IncomingMessage)
        │   handler set by service.New via wa.OnIncomingMessage(s.handleIncoming)
        ▼
service.handleIncoming(msg)
        ├── store.Contacts.Put     (push_name from event)
        ├── store.Chats.Put        (last_msg_at = msg.Timestamp, unread_count++ on inbound)
        └── store.Messages.Put     (kind set; body for text, empty otherwise)
        Errors logged via slog; never returned to whatsmeow.
```

The service constructor's signature changes from `New(WAClient) Service` to `New(WAClient, store.Bundle, *slog.Logger) Service`. `serve.go` already has all three values; the change is mechanical.

## 5. WAClient interface changes

```go
// internal/waclient/waclient.go

// Sent is the envelope returned by SendText: enough information for the caller
// to persist the message as our own outbound row.
type Sent struct {
    ID        string
    Timestamp time.Time
    SenderJID string
}

// IncomingMessage is one received message translated out of whatsmeow's
// *events.Message. Plan 04 covers text and media-kind messages; protocol /
// system events (group state changes etc.) are skipped at the adapter and
// never reach the handler.
type IncomingMessage struct {
    ID        string
    ChatJID   string
    ChatKind  string    // "user" | "group" | "broadcast" | "newsletter"
    SenderJID string
    Timestamp time.Time
    Kind      string    // "text" | "image" | "video" | "audio" | "document" | "sticker"
    Body      string    // empty for non-text
    PushName  string    // sender's display name from the event payload (may be empty)
}

type WAClient interface {
    // Plan 02 surface
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

// ChatKindFromJID classifies a JID by its server suffix. Used by both the
// adapter (translating events) and the service (deriving Chat.Kind on outbound).
func ChatKindFromJID(jid string) string
```

`ChatKindFromJID` rules: `@s.whatsapp.net` → `"user"`, `@g.us` → `"group"`, `@broadcast` → `"broadcast"`, `@newsletter` → `"newsletter"`, anything else → `"unknown"`.

## 6. waclient adapter changes

`whatsmeow_adapter.go` gains:

- `SendText(ctx, chatJID, text)` — parses `chatJID` via `whatsmeow.types.ParseJID`, builds a `*waE2E.Message{Conversation: proto.String(text)}`, calls `client.SendMessage(ctx, parsedJID, msg)`. Returns `Sent{ID: resp.ID, Timestamp: resp.Timestamp, SenderJID: a.client.Store.ID.String()}`. Errors wrapped with `fmt.Errorf("send text: %w", err)`. Returns `ErrNotConnected` (new sentinel) if `client == nil || !client.IsConnected()`.
- `OnIncomingMessage(h)` — stores `h` in `a.incomingHandler` under the existing mutex. Setting twice replaces; setting `nil` clears.
- `onEvent` extended with `*events.Message`: translates to `IncomingMessage` and dispatches under the lock-released call. Determines `Kind` from the protobuf message variant (`Conversation` / `ImageMessage` / `VideoMessage` / `AudioMessage` / `DocumentMessage` / `StickerMessage`); other variants (e.g. `ProtocolMessage`, `ReactionMessage`) skip the call entirely.

A new sentinel:
```go
var ErrNotConnected = errors.New("waclient: not connected")
```

## 7. Service layer changes

```go
// internal/service/service.go

type Service interface {
    Status(ctx context.Context) (waclient.Status, error)
    LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
    LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
    Logout(ctx context.Context) error

    // Plan 04
    SendText(ctx context.Context, chatJID, text string) (store.Message, error)
}

func New(wa waclient.WAClient, bundle store.Bundle, logger *slog.Logger) Service
```

`New` registers `wa.OnIncomingMessage(s.handleIncoming)` before returning, so any message arriving after the daemon is fully booted reaches `handleIncoming`. Messages that arrive between `wa.Resume` and `service.New` are missed; that window is sub-millisecond and Plan 09's events_log will eventually close the gap when it adds resumable streams.

`SendText`:
1. Validate `chatJID != ""` and `text != ""` and `len(text) <= 4096` (WhatsApp's text limit). Returns `ErrInvalidRequest` (new sentinel) on failure.
2. Call `wa.SendText(ctx, chatJID, text)`. Bubble up `waclient.ErrNotConnected` so the handler can map it to 409.
3. Build `store.Message{ID: sent.ID, ChatJID, SenderJID: sent.SenderJID, Timestamp: sent.Timestamp, Kind: "text", Body: text}`. Call `bundle.Messages.Put`. If this fails, log and continue (the whatsmeow echo will heal it).
4. Upsert `chats`: `bundle.Chats.Put({JID: chatJID, Kind: ChatKindFromJID(chatJID), LastMsgAt: sent.Timestamp})`. (Outbound doesn't change `unread_count`.) On chat-Put failure, log and continue.
5. Return the constructed `store.Message`.

`handleIncoming(msg waclient.IncomingMessage)`:
1. `bundle.Contacts.Put({JID: msg.SenderJID, PushName: msg.PushName})` — only if `msg.PushName != ""`. Failure logged.
2. Read existing chat to preserve `unread_count` semantics: `chat, _ := bundle.Chats.Get(ctx, msg.ChatJID)`. If not found, `chat = store.Chat{JID, Kind: msg.ChatKind}`.
3. Update `chat.LastMsgAt = msg.Timestamp`, `chat.UnreadCount++`, `chat.Kind = msg.ChatKind` (in case it was unknown). `bundle.Chats.Put(chat)`. Failure logged.
4. `bundle.Messages.Put({ID, ChatJID, SenderJID, Timestamp, Kind, Body})`. Idempotent because of upsert; whatsmeow re-deliveries on reconnect dedup naturally. Failure logged.

Service-level sentinels:
```go
var (
    ErrInvalidRequest = errors.New("service: invalid request")
)
```

## 8. HTTP handler — `POST /v1/messages`

```go
// internal/transport/http/messages.go

type sendTextRequest struct {
    ChatJID string `json:"chat_jid"`
    Text    string `json:"text"`
}

type sendTextResponse struct {
    ID      string    `json:"id"`
    ChatJID string    `json:"chat_jid"`
    Ts      time.Time `json:"ts"`
}

func SendTextHandler(svc service.Service) http.Handler
```

Routing (router.go): `r.Method(http.MethodPost, "/messages", SendTextHandler(d.Service))` inside the auth-protected group.

Status code mapping:
| Outcome | Status | code |
|---|---|---|
| OK | 201 | — |
| body parse fail / empty fields / text > 4096 | 400 | `request.invalid` |
| `errors.Is(err, waclient.ErrNotConnected)` | 409 | `wa.not_connected` |
| anything else from `service.SendText` | 500 | `wa.send_failed` |

## 9. Wiring into serve

`cmd/whatsmeow-api/serve.go` updates the `service.New(wa)` call to `service.New(wa, appDB.Bundle(), logger)`. The `httpapi.NewServer(httpapi.Deps{...})` continues to pass `Service` and `Store` (Plan 03 already wired the Bundle into Deps; Plan 04 reuses it implicitly via the service rather than via direct handler access). `Deps.Store` remains in place for future plans that want a store handle directly.

## 10. Testing strategy

**Unit — waclient**
- `TestChatKindFromJID` covering each suffix variant.
- No automated test for `SendText` / `OnIncomingMessage` / `onEvent` — they hit real WhatsApp. Manual smoke covers them.

**Unit — service**
- `TestSendTextSuccess` — fake WAClient returns `Sent{...}`; assert `store.Messages.Put` and `store.Chats.Put` called with right values.
- `TestSendTextValidation` — empty chat_jid, empty text, text > 4096 → `ErrInvalidRequest`.
- `TestSendTextNotConnected` — fake WA returns `ErrNotConnected`; service propagates.
- `TestSendTextPersistFailureStillSucceeds` — fake store returns error; service logs and returns the constructed Message.
- `TestHandleIncomingNewChat` — chat doesn't exist; new chat created with `UnreadCount: 1`.
- `TestHandleIncomingExistingChat` — chat exists with `UnreadCount: 3`; becomes 4.
- `TestHandleIncomingNonText` — `Kind: "image"`, body empty; row persisted with `Kind: "image"`.
- `TestHandleIncomingEmptyPushName` — push_name skipped (no contacts.Put call).

Service tests use the existing pattern: a `fakeWA` and a hand-rolled in-memory implementation of `store.Bundle`'s six interfaces.

**Unit — HTTP**
- `TestSendTextHappyPath` — fake Service returns Message; 201 + JSON body match.
- `TestSendTextRejectsEmptyText` — 400 `request.invalid`.
- `TestSendTextRejectsLongText` — 4097-char text → 400 `request.invalid`.
- `TestSendTextNotConnected` — fake Service returns `waclient.ErrNotConnected`; 409.
- `TestSendTextInternalError` — fake Service returns generic error; 500.

**Manual smoke (E2E)**
A new task in the implementation plan, paralleling Plan 02's Task 18:
1. Daemon paired (assume Plan 02's `login qr` has been run).
2. `curl -X POST -H "Content-Type: application/json" -d '{"chat_jid":"+27821234567@s.whatsapp.net","text":"hello from plan 04"}' http://127.0.0.1:8080/v1/messages` → 201 + body.
3. The phone receives the message.
4. Reply from the phone. Wait ~2s.
5. `sqlite3 data/whatsmeow-app.db 'SELECT id, chat_jid, sender_jid, kind, body FROM messages ORDER BY ts DESC LIMIT 5'` shows both rows.
6. `sqlite3 data/whatsmeow-app.db 'SELECT jid, last_msg_at, unread_count FROM chats'` shows the chat with `unread_count >= 1`.

## 11. File layout

New / modified:

```
internal/waclient/
  waclient.go                     +Sent +IncomingMessage +ErrNotConnected +ChatKindFromJID
  waclient_test.go                +TestChatKindFromJID
  whatsmeow_adapter.go            +SendText, OnIncomingMessage, onEvent → events.Message

internal/service/
  service.go                      +SendText, +handleIncoming, New(wa, bundle, logger)
  service_test.go                 +TestSendText*, +TestHandleIncoming*

internal/transport/http/
  messages.go                     +SendTextHandler
  messages_test.go                +TestSendText*
  router.go                       +POST /v1/messages route

cmd/whatsmeow-api/serve.go        service.New(wa, appDB.Bundle(), logger)
README.md                         status section
```

No files removed.

## 12. Dependencies

No new runtime deps. whatsmeow already provides the message types, JID parsing, and `events.Message`. Plan 02 added `google.golang.org/protobuf` transitively (through whatsmeow); we use `proto.String(text)` to build the `*waE2E.Message`.

## 13. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS (existing + Plan 04's new tests).
- Daemon paired with a real WhatsApp account: outbound POST lands a message, inbound message lands in the store. Verified via the manual smoke in §10.
- Existing Plan 01/02/03 smoke (`/v1/health`, `/v1/status`, login flow, app DB schema) continues to pass.

## 14. Open questions deferred to implementation

- Exact whatsmeow message-type detection in `onEvent`: the implementer should `go doc go.mau.fi/whatsmeow/types/events.Message` and `go.mau.fi/whatsmeow/proto/waE2E.Message` to enumerate the relevant variants. The list in §6 (`Conversation` / `ImageMessage` / `VideoMessage` / `AudioMessage` / `DocumentMessage` / `StickerMessage`) is the intent; the exact field names / accessors are confirmed at build time.
- Whether `events.Message` carries a server-timestamp the adapter should prefer over the protobuf-embedded one. Adapter picks the field that whatsmeow documents as authoritative.
- Whether outbound text should round-trip through the store before returning to the client (i.e., persist first, then return), versus the current "send first, persist after, return either way" plan. Current plan optimizes for whatsmeow ack as the canonical event; revisit if the persist-first ordering proves friendlier in practice.

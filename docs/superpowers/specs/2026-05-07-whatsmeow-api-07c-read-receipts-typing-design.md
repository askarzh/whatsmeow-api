# whatsmeow-api Plan 07c — Read Receipts + Typing Design

**Date:** 2026-05-07
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 07b (reactions) — merged.
**Sibling plans:** 07a (replies + edits + deletes) — merged. 07b (reactions) — merged.

## 1. Purpose

Last sibling of the Plan 07 trilogy: read receipts and typing presence.

- **`POST /v1/messages/{id}/read`** — daemon marks a received message as read; whatsmeow sends the receipt; local `chats.unread_count` decrements by 1 (clamped at 0).
- **`POST /v1/chats/{jid}/typing {state}`** — daemon sends "composing" or "paused" presence. No persistence.
- **`GET /v1/messages/{id}/receipts`** — lists all receipts (delivered / read / played) for a message we sent.

Inbound: whatsmeow's `events.Receipt` events populate a new `receipts` table — useful for "did the recipient see this?" queries.

## 2. Goals

- Single-message granularity on the outbound mark-as-read. Bulk endpoints deferred.
- Decrement `chats.unread_count` by 1 per call, clamped at 0. The daemon doesn't track per-message unread state; this is a coarse but stable approximation.
- Inbound receipts cover three types from whatsmeow: `Delivered`, `Read`, `Played`. Other receipt types (Sender, Inactive, Retry, etc.) are skipped at the adapter.
- New `receipts` table mirrors the reactions pattern: PK `(message_id, reader_jid, type)`, FK cascade with `messages`.
- Typing presence is fire-and-forget — outbound only. Inbound typing events are skipped (no SSE in v1 to surface them).

## 3. Non-goals (Plan 07c)

- Bulk mark-as-read (`POST /v1/chats/{jid}/read` to mark all unread). YAGNI; consumers can iterate.
- "Mark up to message X" cumulative semantics. Single-message only.
- Inbound typing presence exposure. No SSE in v1; ephemeral state via REST polling is wrong.
- Receipt types beyond Delivered/Read/Played.
- Computing `chats.unread_count` from receipts. Plan 07c keeps the simple decrement; a future "rebuild unread from receipts" is a separate concern.
- Read-by-others statistics (e.g. "3 of 5 group members read this"). Clients can compute from `ListReceipts`.

## 4. Architecture

```
OUTBOUND
  POST /v1/messages/{id}/read   (body ignored)
        │
        ▼
  service.MarkMessageRead(messageID)
    ├── lookup; 404 if missing
    ├── wa.MarkRead(chatJID, senderJID, messageID, time.Now())
    └── Chats.Get → if UnreadCount > 0: UnreadCount--; Chats.Put
        → 204

  POST /v1/chats/{jid}/typing  body: {"state": "composing"|"paused"}
        │
        ▼
  service.SendTyping(chatJID, state)
    ├── validate state in {composing, paused}; ErrInvalidRequest otherwise
    └── wa.SendChatPresence(chatJID, state)
        → 204
        No persistence.

GET /v1/messages/{id}/receipts
        │
        ▼
  service.ListReceipts(messageID) → bundle.Receipts.ListByMessageID
    → 200 {"receipts": [{message_id, reader_jid, type, ts}, ...]}

INBOUND
  whatsmeow events.Receipt   (separate event type — NOT events.Message)
        │
        ▼
  adapter.onEvent → adapter.receiptHandler(IncomingReceipt)
    handler set by service.New via wa.OnIncomingReceipt(s.handleReceipt)
        │
        ▼
  service.handleReceipt(r):
    for each id in r.MessageIDs:
        bundle.Receipts.Put({MessageID: id, ReaderJID: r.ReaderJID,
                              Type: r.Type, Timestamp: r.Timestamp})
    No chat upsert, no message persistence.
```

## 5. Schema

New migration `internal/store/migrations/sqlite/0003_receipts.{up,down}.sql`.

```sql
-- 0003_receipts.up.sql
CREATE TABLE receipts (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reader_jid TEXT NOT NULL,
    type       TEXT NOT NULL,    -- "delivered" | "read" | "played"
    ts         INTEGER NOT NULL,
    PRIMARY KEY (message_id, reader_jid, type)
);
CREATE INDEX idx_receipts_message ON receipts(message_id);
```

```sql
-- 0003_receipts.down.sql
DROP INDEX IF EXISTS idx_receipts_message;
DROP TABLE IF EXISTS receipts;
```

PK includes `type` so a single reader can have separate "delivered" → "read" rows. FK cascade with `messages`. `ts` is Unix-seconds.

## 6. Store interface changes

```go
// internal/store/store.go
type Receipt struct {
    MessageID string
    ReaderJID string
    Type      string  // "delivered" | "read" | "played"
    Timestamp time.Time
}

type ReceiptStore interface {
    Put(ctx context.Context, r Receipt) error
    ListByMessageID(ctx context.Context, messageID string) ([]Receipt, error)
}

type Bundle struct {
    Chats     ChatStore
    Messages  MessageStore
    Contacts  ContactStore
    Media     MediaStore
    Events    EventsLog
    KV        KV
    Reactions ReactionStore
    Receipts  ReceiptStore // new
}
```

`Put` is upsert: `INSERT … ON CONFLICT(message_id, reader_jid, type) DO UPDATE SET ts = excluded.ts`. No `Delete` — receipts are append-only from the daemon's perspective; the protocol doesn't have "un-read" events.

`ListByMessageID` returns rows ordered by `(reader_jid ASC, type ASC)` for stability. Empty slice (not nil) when no rows.

## 7. SQLite implementation

`internal/store/sqlite/receipts.go`:

```go
type ReceiptStore struct{ db *sql.DB }

const receiptColumns = `message_id, reader_jid, type, ts`

func (s *ReceiptStore) Put(ctx context.Context, r store.Receipt) error {
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO receipts (message_id, reader_jid, type, ts)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(message_id, reader_jid, type) DO UPDATE SET
            ts = excluded.ts
    `, r.MessageID, r.ReaderJID, r.Type, r.Timestamp.Unix())
    if err != nil {
        return fmt.Errorf("receipts put: %w", err)
    }
    return nil
}

func (s *ReceiptStore) ListByMessageID(ctx context.Context, messageID string) ([]store.Receipt, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT `+receiptColumns+` FROM receipts WHERE message_id = ? ORDER BY reader_jid ASC, type ASC`,
        messageID)
    if err != nil {
        return nil, fmt.Errorf("receipts list: %w", err)
    }
    defer rows.Close()
    out := make([]store.Receipt, 0)
    for rows.Next() {
        var (
            r  store.Receipt
            ts int64
        )
        if err := rows.Scan(&r.MessageID, &r.ReaderJID, &r.Type, &ts); err != nil {
            return nil, fmt.Errorf("receipts list scan: %w", err)
        }
        r.Timestamp = unixToTime(ts)
        out = append(out, r)
    }
    return out, rows.Err()
}
```

`internal/store/sqlite/store.go` gains `receipts *ReceiptStore` field; `Bundle()` returns it.

## 8. WAClient interface

```go
// internal/waclient/waclient.go

// MarkRead sends a read receipt for messageID to senderJID in chatJID.
MarkRead(ctx context.Context, chatJID, senderJID, messageID string, timestamp time.Time) error

// SendChatPresence sends typing or paused presence to chatJID.
// state must be "composing" or "paused".
SendChatPresence(ctx context.Context, chatJID, state string) error

// IncomingReceipt is one inbound acknowledgement event for one or more
// of our outbound messages. Plan 07c — separate from IncomingMessage
// because events.Receipt is a distinct whatsmeow event type.
type IncomingReceipt struct {
    MessageIDs []string
    ChatJID    string
    ReaderJID  string
    Type       string  // "delivered" | "read" | "played"
    Timestamp  time.Time
}

// OnIncomingReceipt registers a handler invoked once per inbound receipt event.
OnIncomingReceipt(handler func(IncomingReceipt))
```

## 9. Adapter implementation

`MarkRead`:
```go
func (a *Adapter) MarkRead(ctx context.Context, chatJID, senderJID, messageID string, timestamp time.Time) error {
    a.mu.Lock()
    if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
        a.mu.Unlock()
        return ErrNotConnected
    }
    client := a.client
    a.mu.Unlock()

    chat, err := types.ParseJID(chatJID)
    if err != nil {
        return fmt.Errorf("parse chat_jid: %w", err)
    }
    sender, err := types.ParseJID(senderJID)
    if err != nil {
        return fmt.Errorf("parse sender_jid: %w", err)
    }
    if err := client.MarkRead([]types.MessageID{messageID}, timestamp, chat, sender); err != nil {
        return fmt.Errorf("mark read: %w", err)
    }
    return nil
}
```

`SendChatPresence`:
```go
func (a *Adapter) SendChatPresence(ctx context.Context, chatJID, state string) error {
    a.mu.Lock()
    if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
        a.mu.Unlock()
        return ErrNotConnected
    }
    client := a.client
    a.mu.Unlock()

    to, err := types.ParseJID(chatJID)
    if err != nil {
        return fmt.Errorf("parse chat_jid: %w", err)
    }

    var presence types.ChatPresence
    switch state {
    case "composing":
        presence = types.ChatPresenceComposing
    case "paused":
        presence = types.ChatPresencePaused
    default:
        return fmt.Errorf("unsupported presence state: %q", state)
    }

    if err := client.SendChatPresence(to, presence, types.ChatPresenceMediaText); err != nil {
        return fmt.Errorf("send chat presence: %w", err)
    }
    return nil
}
```

`OnIncomingReceipt`:
```go
func (a *Adapter) OnIncomingReceipt(handler func(IncomingReceipt)) {
    a.mu.Lock()
    a.incomingReceipt = handler
    a.mu.Unlock()
}
```
The Adapter struct gains `incomingReceipt func(IncomingReceipt)`.

`onEvent` extension:
```go
case *events.Receipt:
    r, ok := translateReceipt(evt)
    if !ok {
        return
    }
    a.mu.Lock()
    h := a.incomingReceipt
    a.mu.Unlock()
    if h != nil {
        h(r)
    }
```

`translateReceipt(evt *events.Receipt) (IncomingReceipt, bool)`:
- Map type: `events.ReceiptTypeDelivered` → `"delivered"`, `events.ReceiptTypeRead` → `"read"`, `events.ReceiptTypePlayed` → `"played"`. Other types → return false.
- IDs: convert `evt.MessageIDs` (`[]types.MessageID`) to `[]string` via `string(id)` per element.
- ChatJID: `evt.Chat.String()`. ReaderJID: `evt.Sender.String()`. Timestamp: `evt.Timestamp`.

> Note: whatsmeow's exact `events.Receipt` field names may differ slightly. Run `go doc go.mau.fi/whatsmeow/types/events.Receipt` to verify; adapt if needed.

## 10. Service layer

```go
type Service interface {
    // existing surface
    MarkMessageRead(ctx context.Context, messageID string) error
    SendTyping(ctx context.Context, chatJID, state string) error
    ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error)
}
```

`service.New` extends to register the receipt callback alongside the existing message callback:
```go
s := &svc{...}
wa.OnIncomingMessage(s.handleIncoming)
wa.OnIncomingReceipt(s.handleReceipt)
return s
```

`MarkMessageRead(ctx, messageID)`:
1. Validate `messageID != ""` → `ErrInvalidRequest`.
2. `existing, err := bundle.Messages.Get(ctx, messageID)`. ErrNotFound propagates.
3. `if err := wa.MarkRead(ctx, existing.ChatJID, existing.SenderJID, messageID, time.Now()); err != nil { return err }`.
4. Decrement chat unread (ignore failure):
   ```go
   chat, err := bundle.Chats.Get(ctx, existing.ChatJID)
   if err == nil && chat.UnreadCount > 0 {
       chat.UnreadCount--
       if err := bundle.Chats.Put(ctx, chat); err != nil {
           s.logger.Warn("decrement unread on mark-read failed", ...)
       }
   }
   ```
5. Return nil.

`SendTyping(ctx, chatJID, state)`:
1. Validate `chatJID != ""` and `state in {"composing","paused"}` → `ErrInvalidRequest`.
2. `wa.SendChatPresence(ctx, chatJID, state)`. Return whatever it returns.

`ListReceipts(ctx, messageID)`:
1. Validate.
2. Delegate to `bundle.Receipts.ListByMessageID`.

`handleReceipt(r waclient.IncomingReceipt)`:
```go
ctx := context.Background()
for _, id := range r.MessageIDs {
    if err := s.bundle.Receipts.Put(ctx, store.Receipt{
        MessageID: id,
        ReaderJID: r.ReaderJID,
        Type:      r.Type,
        Timestamp: r.Timestamp,
    }); err != nil {
        s.logger.Warn("persist receipt failed", "id", id, "type", r.Type, "err", err)
    }
}
```

## 11. HTTP

`internal/transport/http/receipts.go` (new):
```go
// MarkReadHandler handles POST /v1/messages/{id}/read.
// Request body is ignored. 204 success.
func MarkReadHandler(svc service.Service) http.Handler

// ListReceiptsHandler handles GET /v1/messages/{id}/receipts.
// 200 {"receipts": [...]}
func ListReceiptsHandler(svc service.Service) http.Handler
```

`internal/transport/http/typing.go` (new):
```go
type sendTypingRequest struct {
    State string `json:"state"`
}

// SendTypingHandler handles POST /v1/chats/{jid}/typing.
// Body: {"state": "composing"|"paused"}. 204 success.
func SendTypingHandler(svc service.Service) http.Handler
```

Routes (auth-protected group):
```go
r.Method(http.MethodPost, "/messages/{id}/read", MarkReadHandler(d.Service))
r.Method(http.MethodGet,  "/messages/{id}/receipts", ListReceiptsHandler(d.Service))
r.Method(http.MethodPost, "/chats/{jid}/typing", SendTypingHandler(d.Service))
```

Status mapping:
- `MarkRead`: 204 / 400 (`request.invalid`) / 404 (`message.not_found`) / 409 (`wa.not_connected`) / 500
- `SendTyping`: 204 / 400 (bad JSON, bad state) / 409 / 500
- `ListReceipts`: 200 with `{"receipts":[...]}` (empty array possible) / 400 / 500

Per-receipt JSON shape:
```json
{"message_id": "...", "reader_jid": "...", "type": "read", "ts": "2026-05-07T12:34:56Z"}
```

## 12. Wiring

`cmd/whatsmeow-api/serve.go` is unchanged.

`internal/store/sqlite/store.go` constructs `*ReceiptStore` and exposes via `Bundle()`. Same pattern as Plan 07b.

In-memory bundle helper in `internal/service/service_test.go` gets a 6th return value (`*receiptStore`) — every existing call site updates to `bundle, chats, msgs, contacts, _, _ := newInMemoryBundle()`.

## 13. Testing strategy

**SQLite** (`receipts_test.go`):
- TestReceiptPutGetList — multiple receipts on one message; multiple types per reader; sort order.
- TestReceiptPutIsUpsert — `(message, reader, type)` collision; second ts wins.
- TestReceiptListEmpty.
- TestReceiptFKCascade — hard-DELETE parent message → receipts cascade.

**Service** (`service_test.go`):
- TestMarkMessageReadHappyPath — fake WA captures args; chat.UnreadCount goes from 5 to 4.
- TestMarkMessageReadDecrementClampsAtZero — chat at 0 stays at 0.
- TestMarkMessageReadNotFound, NotConnected, Validation.
- TestSendTypingComposing, TestSendTypingPaused — fake WA captures state.
- TestSendTypingValidationBadState, TestSendTypingValidationEmptyChatJID, TestSendTypingNotConnected.
- TestListReceiptsHappyPath / Validation.
- TestHandleReceiptPersistsAll — IncomingReceipt with 3 messageIDs + Type=read → 3 rows inserted.
- TestHandleReceiptUpsert — same (message, reader, type) twice → only 1 row, latest ts.

**HTTP**:
- `receipts_test.go`: POST /read 204 / 404 / 409. GET /receipts 200 with shape; empty array.
- `typing_test.go`: POST /typing 204; bad state → 400; bad JSON → 400; 409.

## 14. File layout

```
internal/store/
  store.go                              +Receipt, +ReceiptStore, +Bundle.Receipts
  migrations/sqlite/0003_receipts.up.sql (new)
  migrations/sqlite/0003_receipts.down.sql (new)
  sqlite/receipts.go (new)
  sqlite/receipts_test.go (new)
  sqlite/store.go                       wire ReceiptStore into Bundle

internal/waclient/
  waclient.go                           +MarkRead, +SendChatPresence, +OnIncomingReceipt; +IncomingReceipt
  whatsmeow_adapter.go                  impls + onEvent *events.Receipt + translateReceipt;
                                         +incomingReceipt field on Adapter

internal/service/
  service.go                            +MarkMessageRead, +SendTyping, +ListReceipts, +handleReceipt;
                                        New registers OnIncomingReceipt
  service_test.go                       new tests; +receiptStore fake; bundle helper now returns 6 values; bridge fakes

internal/transport/http/
  receipts.go (new)                     MarkReadHandler, ListReceiptsHandler
  receipts_test.go (new)
  typing.go (new)                       SendTypingHandler
  typing_test.go (new)
  router.go                             +3 routes

README.md                               status section
```

The 9 existing HTTP fake services need stubs for the 3 new Service methods.

## 15. Dependencies

None added. `Client.MarkRead` and `Client.SendChatPresence` are in whatsmeow already. `events.Receipt` and `types.ChatPresence` constants are in their respective whatsmeow packages.

## 16. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new sqlite + service + HTTP tests.
- Daemon boots after fresh checkout: `data/whatsmeow-app.db` gains the `receipts` table on first run via migration `0003_receipts.up.sql`.
- Manual smoke against paired account:
  - Mark-read: receive a message; `curl -X POST .../v1/messages/<id>/read` → 204; `chat.unread_count` decrements; recipient (sender) sees the read receipt.
  - Typing: `curl -X POST -d '{"state":"composing"}' .../v1/chats/<jid>/typing` → 204; recipient sees "composing…" indicator briefly.
  - Receipts: send a message; recipient reads it; `curl .../v1/messages/<id>/receipts` shows a row with type=read.
- Existing Plan 01–07b endpoints unchanged.

## 17. Open questions deferred to implementation

- Exact whatsmeow `events.Receipt` field names. The implementer runs `go doc go.mau.fi/whatsmeow/types/events.Receipt` to verify; the adapter adapts if needed.
- Whether `Client.MarkRead`'s signature is exactly `([]types.MessageID, time.Time, types.JID, types.JID) error`. Verify and adapt.
- Whether `types.ChatPresence` is an enum or a string. Verify and adapt.
- Whether `Client.SendChatPresence` requires a media argument (we pass `types.ChatPresenceMediaText`). Verify.
- Whether to also persist the `Sender` receipt type (some whatsmeow configurations emit a "sender" receipt for our own sent messages). Plan 07c skips it; revisit if a consumer needs to distinguish.

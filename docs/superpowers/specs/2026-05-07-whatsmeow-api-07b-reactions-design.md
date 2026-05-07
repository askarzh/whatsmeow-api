# whatsmeow-api Plan 07b — Reactions Design

**Date:** 2026-05-07
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 07a (replies + edits + deletes) — merged.
**Sibling plans:** 07a (replies + edits + deletes) — merged. 07c (read receipts + typing) — to be brainstormed.

## 1. Purpose

Bidirectional emoji reactions on messages. Outbound `POST /v1/messages/{id}/reactions {emoji}` (empty emoji clears). Inbound `*waE2E.ReactionMessage` events are persisted to a new `reactions` table. A read endpoint `GET /v1/messages/{id}/reactions` returns the list.

## 2. Goals

- One reaction per `(message_id, sender_jid)` — matches WhatsApp's rule. PK enforces it.
- Setting and clearing reactions go through the same outbound flow with empty emoji as the "clear" signal.
- Self-sent reactions persist immediately on outbound success; the whatsmeow echo (which arrives as `IsFromMe == true`) is filtered by Plan 04's existing `IsFromMe` check, so we don't double-apply.
- Read endpoint exists from day one — without it the persisted data is invisible to clients.
- New schema migration `0002_reactions` is the only schema change.

## 3. Non-goals (Plan 07b)

- Reactions embedded in `GET /v1/chats/{jid}/messages` response. Plan 05's response shape stays. Clients fetch reactions separately if they want them.
- Bulk reactions endpoint.
- Reaction history. We store only the current reaction per `(message_id, sender_jid)`; setting a new emoji overwrites; clearing deletes the row.
- Group reaction summaries (e.g. "3 people reacted with 👍"). Clients can compute from the list.
- Reactions to media-only messages — no special-case; the schema treats all messages the same.

## 4. Architecture

```
OUTBOUND
  POST /v1/messages/{id}/reactions  body: {"emoji": "👍"}
        │
        ▼
  service.SendReaction(messageID, emoji)
        ├── validate messageID != ""
        ├── existing := bundle.Messages.Get(messageID)         (404 if missing)
        ├── wa.SendReaction(existing.ChatJID, messageID, emoji)
        ├── ourJID := *wa.Status().JID
        ├── if emoji == "":
        │     bundle.Reactions.Delete(messageID, ourJID)
        ├── else:
        │     bundle.Reactions.Put({messageID, ourJID, emoji, time.Now()})
        └── return nil → 204

GET /v1/messages/{id}/reactions
        │
        ▼
  service.ListReactions(messageID) → bundle.Reactions.ListByMessageID
        → 200 {"reactions": [{message_id, sender_jid, emoji, ts}, ...]}

INBOUND
  whatsmeow events.Message with *waE2E.ReactionMessage
        │
        ▼
  adapter.translateIncoming detects, returns:
      IncomingMessage{ReactionTargetID: rm.GetKey().GetID(), ReactionEmoji: rm.GetText()}
        │
        ▼
  service.handleIncoming routes (BEFORE Plan 07a revoke/edit branches):
    - emoji != "": Reactions.Put({target, sender, emoji, ts})
    - emoji == "": Reactions.Delete(target, sender)
    No unread bump, no chat upsert, no message persistence.
```

## 5. Schema

New migration `internal/store/migrations/sqlite/0002_reactions.{up,down}.sql`.

```sql
-- 0002_reactions.up.sql
CREATE TABLE reactions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    sender_jid TEXT NOT NULL,
    emoji      TEXT NOT NULL,
    ts         INTEGER NOT NULL,
    PRIMARY KEY (message_id, sender_jid)
);
CREATE INDEX idx_reactions_message ON reactions(message_id);
```

```sql
-- 0002_reactions.down.sql
DROP INDEX IF EXISTS idx_reactions_message;
DROP TABLE IF EXISTS reactions;
```

`emoji` is stored as the literal Unicode string (e.g. `"👍"`). `ts` is Unix-seconds. The PK enforces "one reaction per user per message"; an upsert via `INSERT … ON CONFLICT(message_id, sender_jid) DO UPDATE SET emoji = excluded.emoji, ts = excluded.ts` handles re-reactions. Clearing a reaction deletes the row (no "empty-emoji row" persisted).

FK cascade: hard-deleting a `messages` row removes its reactions. Soft-deleting (Plan 03 + Plan 07a) does NOT cascade — reactions on a soft-deleted message remain. Acceptable: the message row stays, just with `deleted_at` set; reactions still reference a real row.

## 6. Store interface changes

```go
// internal/store/store.go

type Reaction struct {
    MessageID string
    SenderJID string
    Emoji     string
    Timestamp time.Time
}

type ReactionStore interface {
    Put(ctx context.Context, r Reaction) error
    Delete(ctx context.Context, messageID, senderJID string) error
    ListByMessageID(ctx context.Context, messageID string) ([]Reaction, error)
}

type Bundle struct {
    Chats     ChatStore
    Messages  MessageStore
    Contacts  ContactStore
    Media     MediaStore
    Events    EventsLog
    KV        KV
    Reactions ReactionStore // new
}
```

`Put` is upsert. `Delete` is idempotent — deleting a non-existent (message_id, sender_jid) returns nil. `ListByMessageID` returns an empty slice (not nil) when there are no reactions; ordered by `(sender_jid ASC)` for stability.

## 7. SQLite implementation

`internal/store/sqlite/reactions.go`:

```go
type ReactionStore struct{ db *sql.DB }

const reactionColumns = `message_id, sender_jid, emoji, ts`

func (s *ReactionStore) Put(ctx context.Context, r store.Reaction) error {
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO reactions (message_id, sender_jid, emoji, ts)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(message_id, sender_jid) DO UPDATE SET
            emoji = excluded.emoji,
            ts    = excluded.ts
    `, r.MessageID, r.SenderJID, r.Emoji, r.Timestamp.Unix())
    if err != nil {
        return fmt.Errorf("reactions put: %w", err)
    }
    return nil
}

func (s *ReactionStore) Delete(ctx context.Context, messageID, senderJID string) error {
    _, err := s.db.ExecContext(ctx,
        `DELETE FROM reactions WHERE message_id = ? AND sender_jid = ?`,
        messageID, senderJID)
    if err != nil {
        return fmt.Errorf("reactions delete: %w", err)
    }
    return nil
}

func (s *ReactionStore) ListByMessageID(ctx context.Context, messageID string) ([]store.Reaction, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT `+reactionColumns+` FROM reactions WHERE message_id = ? ORDER BY sender_jid ASC`,
        messageID)
    if err != nil {
        return nil, fmt.Errorf("reactions list: %w", err)
    }
    defer rows.Close()
    out := make([]store.Reaction, 0)
    for rows.Next() {
        var (
            r  store.Reaction
            ts int64
        )
        if err := rows.Scan(&r.MessageID, &r.SenderJID, &r.Emoji, &ts); err != nil {
            return nil, fmt.Errorf("reactions list scan: %w", err)
        }
        r.Timestamp = unixToTime(ts)
        out = append(out, r)
    }
    return out, rows.Err()
}
```

`internal/store/sqlite/store.go` gains a `Reactions *ReactionStore` field and the `Bundle()` method returns it.

## 8. WAClient interface changes

```go
// internal/waclient/waclient.go

// IncomingMessage gains two more optional fields. Mutually exclusive with
// EditOfID/RevokeOfID — a single event is at most one of: text/media,
// edit, revoke, reaction.
type IncomingMessage struct {
    // existing fields
    ReactionTargetID string  // if set, this event is a reaction targeting that message
    ReactionEmoji    string  // empty string means "clear my reaction"
}

type WAClient interface {
    // existing surface
    SendReaction(ctx context.Context, chatJID, originalMessageID, emoji string) error
}
```

## 9. Adapter implementation

`SendReaction(ctx, chatJID, originalID, emoji)`:

- Connection check (same pattern as SendText/SendEdit/SendRevoke) — return `ErrNotConnected` if not connected.
- `to, err := types.ParseJID(chatJID)`.
- `senderJID := *a.client.Store.ID` (own JID, dereferenced).
- Whatsmeow has `Client.BuildReaction(chat, sender types.JID, id types.MessageID, emoji string) *waE2E.Message` — use it. Construct, then `client.SendMessage(ctx, to, msg)`.
- The returned `Sent` envelope isn't returned to the service (reactions don't generate a useful message id for the caller). Return nil on success.

> Note: if `BuildReaction` doesn't exist or has a different signature, build manually:
> ```go
> &waE2E.Message{
>     ReactionMessage: &waE2E.ReactionMessage{
>         Key: &waCommon.MessageKey{
>             FromMe:    proto.Bool(false), // true if reacting to our own message; false otherwise
>             ID:        proto.String(originalID),
>             RemoteJID: proto.String(chatJID),
>         },
>         Text:               proto.String(emoji),
>         SenderTimestampMS:  proto.Int64(time.Now().UnixMilli()),
>     },
> }
> ```
> The implementer verifies via `go doc go.mau.fi/whatsmeow.Client.BuildReaction`.

`translateIncoming` extension: when `evt.Message.ReactionMessage != nil` AND `evt.Info.IsFromMe == false`, return:
```go
return IncomingMessage{
    ID:               evt.Info.ID,
    ChatJID:          evt.Info.Chat.String(),
    ChatKind:         ChatKindFromJID(evt.Info.Chat.String()),
    SenderJID:        evt.Info.Sender.String(),
    Timestamp:        evt.Info.Timestamp,
    ReactionTargetID: evt.Message.ReactionMessage.GetKey().GetID(),
    ReactionEmoji:    evt.Message.ReactionMessage.GetText(),
}, true
```

The existing `IsFromMe` guard at the top of `translateIncoming` already filters our own reaction echoes — no extra logic needed.

## 10. Service layer

```go
type Service interface {
    // existing surface
    SendReaction(ctx context.Context, messageID, emoji string) error
    ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error)
}
```

`SendReaction(ctx, messageID, emoji)`:
1. Validate `strings.TrimSpace(messageID) != ""` → else `ErrInvalidRequest`.
2. `existing, err := bundle.Messages.Get(ctx, messageID)`. `ErrNotFound` propagates → 404.
3. `if err := wa.SendReaction(ctx, existing.ChatJID, messageID, emoji); err != nil { return err }`.
4. `ourJID := ""`. If `wa.Status().JID != nil { ourJID = *wa.Status().JID }`.
5. If `emoji == ""`:
   `if err := bundle.Reactions.Delete(ctx, messageID, ourJID); err != nil { s.logger.Warn("reactions delete failed", ...) }`.
   Else:
   `if err := bundle.Reactions.Put(ctx, store.Reaction{MessageID: messageID, SenderJID: ourJID, Emoji: emoji, Timestamp: time.Now()}); err != nil { s.logger.Warn("reactions put failed", ...) }`.
6. Return nil.

`ListReactions(ctx, messageID)`:
1. Validate `messageID != ""` → `ErrInvalidRequest`.
2. Delegate to `bundle.Reactions.ListByMessageID`. Empty slice is a valid result (no need to check the message exists).

`handleIncoming` extension (NEW branch, BEFORE Plan 07a's revoke/edit branches):
```go
if msg.ReactionTargetID != "" {
    if msg.ReactionEmoji == "" {
        if err := s.bundle.Reactions.Delete(ctx, msg.ReactionTargetID, msg.SenderJID); err != nil {
            s.logger.Warn("clear reaction on incoming failed", "target", msg.ReactionTargetID, "err", err)
        }
    } else {
        if err := s.bundle.Reactions.Put(ctx, store.Reaction{
            MessageID: msg.ReactionTargetID,
            SenderJID: msg.SenderJID,
            Emoji:     msg.ReactionEmoji,
            Timestamp: msg.Timestamp,
        }); err != nil {
            s.logger.Warn("persist reaction on incoming failed", "target", msg.ReactionTargetID, "err", err)
        }
    }
    return
}
```

## 11. HTTP

New file `internal/transport/http/reactions.go`:

```go
type sendReactionRequest struct {
    Emoji string `json:"emoji"`
}

// POST /v1/messages/{id}/reactions
func SendReactionHandler(svc service.Service) http.Handler

// GET /v1/messages/{id}/reactions
// 200 {"reactions": [{message_id, sender_jid, emoji, ts}, ...]}
func ListReactionsHandler(svc service.Service) http.Handler
```

Status mapping for SendReaction:
- 204 success
- 400 `request.invalid` — bad JSON
- 404 `message.not_found` — `errors.Is(err, store.ErrNotFound)`
- 409 `wa.not_connected`
- 500 default

Status mapping for ListReactions:
- 200 with `{"reactions": [...]}`
- 400 `request.invalid` — empty message_id (chi shouldn't allow)
- 500 default

Routes (auth-protected group):
```go
r.Method(http.MethodPost, "/messages/{id}/reactions", SendReactionHandler(d.Service))
r.Method(http.MethodGet,  "/messages/{id}/reactions", ListReactionsHandler(d.Service))
```

Per-reaction JSON shape:
```json
{"message_id": "...", "sender_jid": "...", "emoji": "👍", "ts": "2026-05-07T12:34:56Z"}
```

## 12. Wiring

`cmd/whatsmeow-api/serve.go` is unchanged. `Service.New` signature stays the same — the `Bundle` it receives now has a `Reactions` field, which the service uses internally.

`internal/store/sqlite/store.go` gets a `Reactions *ReactionStore` field; constructor wires it; `Bundle()` returns it.

In-memory bundle helpers in `internal/service/service_test.go` get a new `reactionStore` fake (map-keyed by `messageID + "|" + senderJID`).

## 13. Testing strategy

**SQLite** (`internal/store/sqlite/reactions_test.go`):
- TestReactionPutGetList: Put two reactions on same message from different senders; ListByMessageID returns both, ordered by sender_jid.
- TestReactionPutIsUpsert: Put twice with same key, second emoji + ts wins.
- TestReactionDelete: Delete removes the row; Delete on missing key is no-op.
- TestReactionFKCascade: hard-DELETE the parent message row → reactions cascade away.
- TestReactionListEmpty: list on a message with no reactions returns empty slice.

**Service** (`internal/service/service_test.go`):
- TestSendReactionHappyPath: fake WA captures emoji; ReactionStore.Put called with our JID.
- TestSendReactionClear: emoji="" → ReactionStore.Delete called; Put NOT called.
- TestSendReactionNotFound: message doesn't exist → `ErrNotFound`.
- TestSendReactionNotConnected: fake WA returns `ErrNotConnected`; service propagates; Reactions store NOT touched.
- TestSendReactionValidation: empty messageID → `ErrInvalidRequest`.
- TestListReactionsHappyPath: seed two reactions; service returns them.
- TestListReactionsValidation: empty messageID → `ErrInvalidRequest`.
- TestHandleIncomingReaction: IncomingMessage with ReactionTargetID + emoji → row appears, no chat unread bump.
- TestHandleIncomingReactionClear: emoji="" → existing row removed.

**HTTP** (`internal/transport/http/reactions_test.go`):
- TestSendReactionHappyPath: 204; fake captures messageID + emoji.
- TestSendReactionEmptyClears: 204; fake captures emoji="".
- TestSendReactionBadJSON: 400.
- TestSendReactionNotFound: 404.
- TestSendReactionNotConnected: 409.
- TestListReactionsHappyPath: 200 + JSON shape.

## 14. File layout

```
internal/store/
  store.go                              +Reaction, +ReactionStore, +Bundle.Reactions
  migrations/sqlite/0002_reactions.up.sql (new)
  migrations/sqlite/0002_reactions.down.sql (new)
  sqlite/reactions.go (new)
  sqlite/reactions_test.go (new)
  sqlite/store.go                       build ReactionStore + Bundle.Reactions

internal/waclient/
  waclient.go                           +SendReaction; +ReactionTargetID, +ReactionEmoji
  whatsmeow_adapter.go                  SendReaction impl; translateIncoming reaction branch

internal/service/
  service.go                            +SendReaction, +ListReactions; handleIncoming reaction branch
  service_test.go                       extend bundle helper with reactionStore; new tests; bridge fakes

internal/transport/http/
  reactions.go (new)                    SendReactionHandler, ListReactionsHandler
  reactions_test.go (new)
  router.go                             +2 routes

README.md                               status section
```

The 9 existing HTTP fake services (status_test, login_qr_test, etc.) need new SendReaction + ListReactions stubs to satisfy the extended Service interface — same bridge pattern as Plan 07a Task 5.

## 15. Dependencies

None added. `Client.BuildReaction` is in whatsmeow already. The `*waE2E.ReactionMessage` proto type is in `go.mau.fi/whatsmeow/proto/waE2E`.

## 16. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new sqlite + service + HTTP tests.
- Daemon boots after fresh checkout: `data/whatsmeow-app.db` gains the `reactions` table on first run (golang-migrate runs `0002_reactions.up.sql`). For existing databases from earlier plans, the migration runs automatically on next `serve`.
- Manual smoke against paired account:
  - React: `curl -X POST -d '{"emoji":"👍"}' .../v1/messages/<id>/reactions` → recipient sees the reaction. Local `reactions` row exists.
  - Clear: `curl -X POST -d '{"emoji":""}' ...` → reaction disappears on recipient. Local row gone.
  - Inbound: another phone reacts to a message we sent → daemon's `reactions` row appears within ~3s. They clear → row disappears.
  - Read: `curl .../v1/messages/<id>/reactions` returns the current list.
- Existing Plan 01–07a endpoints unchanged.

## 17. Open questions deferred to implementation

- Whether `Client.BuildReaction` exists in the installed whatsmeow version. The implementer runs `go doc go.mau.fi/whatsmeow.Client.BuildReaction`; if absent, build the proto manually as shown in §9.
- Whether to validate emoji length / character set. Plan 07b accepts any non-empty string; whatsmeow's protocol limits aren't enforced client-side.
- Whether self-reactions to our own messages should be allowed. Plan 07b says yes (no special case). WhatsApp's UI allows it.

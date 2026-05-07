# whatsmeow-api Plan 07a — Replies + Edits + Deletes Design

**Date:** 2026-05-07
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 06 (media) — merged.
**Sibling plans:** 07b (reactions), 07c (read receipts + typing) — to be brainstormed separately.

## 1. Purpose

Three message-mutation flows on top of Plan 04's send/receive:

- **Replies** — extend `POST /v1/messages` with an optional `reply_to` message id; the daemon sends a quoted reply via whatsmeow.
- **Edits** — `PATCH /v1/messages/{id}` with a new text body; the daemon sends a `MESSAGE_EDIT` ProtocolMessage and updates the local row.
- **Deletes** — `DELETE /v1/messages/{id}`; the daemon sends a `REVOKE` ProtocolMessage and soft-deletes the local row.

Inbound: whatsmeow `events.Message` events with `ProtocolMessage` variants (edit, revoke) update the corresponding stored rows. Plan 04's existing `handleIncoming` is extended with two new branches.

No schema changes — Plan 03's `messages.reply_to`, `edited_at`, `deleted_at` columns already exist.

## 2. Goals

- All three flows are bidirectional. Outbound mutates whatsmeow + local store. Inbound translates whatsmeow events into local-store mutations.
- Edits and deletes are owner-only at the API: the daemon's own JID must equal the message's `sender_jid` or the request is rejected with 403. WhatsApp itself enforces this on the protocol level too — we surface the failure earlier.
- Edit and delete return idempotent-ish behavior: editing a message to the same text is allowed; deleting an already-deleted message returns 404 (the row exists but `deleted_at != nil` is treated as "no longer addressable").
- Replies use whatsmeow's `ContextInfo.StanzaID` mechanism. The daemon does not need to look up the original message body — recipients resolve the quote from their own store.
- The HTTP shape is RESTful: PATCH for edit, DELETE for delete. The master design's "edit/delete via fields on POST /v1/messages" wording predates the API maturing and is superseded.

## 3. Non-goals (Plan 07a)

- Reactions → Plan 07b.
- Read receipts and typing → Plan 07c.
- Editing media messages (caption updates). whatsmeow supports a media-edit proto variant; Plan 07a only ships text-body edits. Sibling plan if consumers need it.
- "Delete for me only" (local hide without revoking on WhatsApp). Plan 07a always revokes for everyone via whatsmeow's `REVOKE` ProtocolMessage.
- Bulk operations.
- Edit history (we overwrite `body` and bump `edited_at`; previous versions are not preserved).
- Editing or deleting messages older than WhatsApp's edit window (~15 minutes for edits, ~2 days for revokes). The daemon doesn't enforce these; it forwards to whatsmeow and surfaces whatever error WhatsApp returns. A 500 with a wrapped error is acceptable in v1.

## 4. Architecture

```
OUTBOUND
  POST /v1/messages {chat_jid, text, reply_to?}
        │
        ▼
  service.SendText(chatJID, text, replyTo)
        ├── waclient.SendText(...) (extended with replyTo)
        ├── persist message + chat upsert (Plan 04 path; replyTo lands in messages.reply_to)

  PATCH /v1/messages/{id} {text}
        │
        ▼
  service.EditMessage(messageID, newText)
        ├── lookup messages.Get(messageID); 404 if not found
        ├── ownership check: sender_jid == our JID; 403 otherwise
        ├── wa.SendEdit(chatJID, messageID, newText)
        ├── update messages: body=newText, edited_at=sent.Timestamp; Messages.Put
        └── return updated store.Message

  DELETE /v1/messages/{id}
        │
        ▼
  service.DeleteMessage(messageID)
        ├── lookup; 404 / 403 same checks
        ├── wa.SendRevoke(chatJID, messageID)
        ├── Messages.SoftDelete(messageID, time.Now())
        └── return nil → 204

INBOUND
  whatsmeow events.Message with *waE2E.ProtocolMessage
        ▼
  adapter.translateIncoming detects:
    - Type=REVOKE → IncomingMessage{RevokeOfID: key.id}
    - Type=MESSAGE_EDIT → IncomingMessage{EditOfID: key.id, Body: newText}
        ▼
  service.handleIncoming routes:
    - if RevokeOfID != "": Messages.SoftDelete(RevokeOfID, msg.Timestamp); return
    - if EditOfID != "": existing := Messages.Get(EditOfID); update body + edited_at; Put; return
    - else: Plan 04 existing path
```

## 5. WAClient interface changes

```go
// internal/waclient/waclient.go

// SendText now takes replyTo (empty string = not a reply).
SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error)

// New
SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error)
SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error)

// IncomingMessage gains two optional ID fields.
type IncomingMessage struct {
    // existing fields (Plan 04 + 06)
    EditOfID   string
    RevokeOfID string
}
```

The `replyTo` parameter is a breaking signature change for `SendText`. There's exactly one caller (service.SendText) so the migration is mechanical: pass `""` from the existing call site, then thread the new HTTP field through in the same task.

For inbound: edit events carry the new text in `m.ProtocolMessage.EditedMessage.Conversation` (or whatsmeow's helper `m.ProtocolMessage.GetEditedMessage()`); the adapter populates `Body` from there. Revoke events carry only the message key; `Body` stays empty.

## 6. waclient adapter implementation

`SendText(ctx, chatJID, text, replyTo)`:

- Existing path when `replyTo == ""`: `&waE2E.Message{Conversation: proto.String(text)}`.
- New path when `replyTo != ""`:
  ```go
  &waE2E.Message{
      ExtendedTextMessage: &waE2E.ExtendedTextMessage{
          Text: proto.String(text),
          ContextInfo: &waE2E.ContextInfo{
              StanzaID: proto.String(replyTo),
              // Participant left empty: receiver's WhatsApp client resolves
              // the quote from its own store. If the receiver doesn't have
              // the original cached, the quote renders as "(message not found)"
              // — acceptable for a v1 reply.
          },
      },
  }
  ```

`SendEdit(ctx, chatJID, originalMessageID, newText)`:

- Run `go doc go.mau.fi/whatsmeow.Client.BuildEdit` first. If a helper exists, use it.
- Otherwise build manually:
  ```go
  &waE2E.Message{
      ProtocolMessage: &waE2E.ProtocolMessage{
          Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
          Key: &waCommon.MessageKey{
              FromMe:    proto.Bool(true),
              ID:        proto.String(originalMessageID),
              RemoteJID: proto.String(chatJID),
          },
          EditedMessage: &waE2E.Message{Conversation: proto.String(newText)},
          TimestampMS:   proto.Int64(time.Now().UnixMilli()),
      },
  }
  ```
- `client.SendMessage(ctx, parsedJID, msg)`.

`SendRevoke(ctx, chatJID, originalMessageID)`:

- whatsmeow has `Client.BuildRevoke(chatJID, senderJID, originalID)` — use it. The senderJID is `client.Store.ID.String()` (our own).
- `client.SendMessage(ctx, parsedJID, revokeMsg)`.

`translateIncoming` extended branch when `m.ProtocolMessage != nil`:

```go
pm := m.ProtocolMessage
switch pm.GetType() {
case waE2E.ProtocolMessage_REVOKE:
    return IncomingMessage{
        ID:         evt.Info.ID,
        ChatJID:    evt.Info.Chat.String(),
        ChatKind:   ChatKindFromJID(evt.Info.Chat.String()),
        SenderJID:  evt.Info.Sender.String(),
        Timestamp:  evt.Info.Timestamp,
        RevokeOfID: pm.GetKey().GetID(),
    }, true
case waE2E.ProtocolMessage_MESSAGE_EDIT:
    edited := pm.GetEditedMessage()
    body := ""
    if edited != nil && edited.GetConversation() != "" {
        body = edited.GetConversation()
    }
    return IncomingMessage{
        ID:        evt.Info.ID,
        ChatJID:   evt.Info.Chat.String(),
        ChatKind:  ChatKindFromJID(evt.Info.Chat.String()),
        SenderJID: evt.Info.Sender.String(),
        Timestamp: evt.Info.Timestamp,
        Body:      body,
        EditOfID:  pm.GetKey().GetID(),
    }, true
default:
    // Other ProtocolMessage types (READ_RECEIPT, etc.) skip in Plan 07a.
    return IncomingMessage{}, false
}
```

Self-sent edits/revokes (where `evt.Info.IsFromMe == true`) are still filtered by Plan 04's existing IsFromMe check — we don't double-apply our own outbound revoke.

## 7. Service layer

```go
type Service interface {
    // existing surface

    // Plan 07a — SendText extended; two new methods
    SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error)
    EditMessage(ctx context.Context, messageID, newText string) (store.Message, error)
    DeleteMessage(ctx context.Context, messageID string) error
}

var ErrForbidden = errors.New("service: forbidden")
```

`SendText`:
- Add validation: if `replyTo != ""` and `len(replyTo) > 200` → `ErrInvalidRequest`. (WhatsApp message IDs are short, anything longer is bogus.)
- Pass `replyTo` to `wa.SendText`.
- Persistence: `store.Message{..., ReplyTo: replyTo}` (the column already exists).

`EditMessage(ctx, messageID, newText)`:
1. Validate: `messageID != ""`, `newText != ""`, `len(newText) <= 4096` → else `ErrInvalidRequest`.
2. `existing, err := bundle.Messages.Get(ctx, messageID)`. `ErrNotFound` propagates.
3. Owner check: get our JID from `wa.Status()`. If `Status().JID == nil || existing.SenderJID != *Status().JID` → `ErrForbidden`.
4. If `existing.DeletedAt != nil` → `ErrForbidden` (cannot edit a deleted message; treat as forbidden rather than not-found because the row still exists).
5. `sent, err := wa.SendEdit(ctx, existing.ChatJID, messageID, newText)`. Error propagates (wraps `ErrNotConnected`).
6. Update local: `existing.Body = newText; t := sent.Timestamp; existing.EditedAt = &t`. `bundle.Messages.Put(ctx, existing)`. Persist failure logged not propagated (consistent with Plan 04 pattern).
7. Return `existing`.

`DeleteMessage(ctx, messageID)`:
1. Validate `messageID != ""`.
2. Lookup, owner check, deleted check (same as Edit).
3. `_, err := wa.SendRevoke(ctx, existing.ChatJID, messageID)`. Error propagates.
4. `now := time.Now()`. `bundle.Messages.SoftDelete(ctx, messageID, now)`. Failure logged.
5. Return nil.

`handleIncoming` extension (added BEFORE the existing path):
```go
if msg.RevokeOfID != "" {
    if err := s.bundle.Messages.SoftDelete(ctx, msg.RevokeOfID, msg.Timestamp); err != nil {
        s.logger.Warn("soft-delete on incoming revoke failed", "id", msg.RevokeOfID, "err", err)
    }
    return
}
if msg.EditOfID != "" {
    existing, err := s.bundle.Messages.Get(ctx, msg.EditOfID)
    if err != nil {
        s.logger.Warn("incoming edit references unknown message", "id", msg.EditOfID, "err", err)
        return
    }
    existing.Body = msg.Body
    t := msg.Timestamp
    existing.EditedAt = &t
    if err := s.bundle.Messages.Put(ctx, existing); err != nil {
        s.logger.Warn("persist incoming edit failed", "id", msg.EditOfID, "err", err)
    }
    return
}
// existing Plan 04 path for normal messages
```

## 8. HTTP

`internal/transport/http/messages.go` modifications:

**SendTextHandler** request struct gains `ReplyTo string \`json:"reply_to,omitempty"\``. Pass through to `service.SendText`. No new validation in the handler (service validates).

**EditMessageHandler** (new):
```go
// PATCH /v1/messages/{id}
// Body: {"text": "..."}
// 200 with the updated message JSON (same shape as POST /v1/messages response, but full message).
func EditMessageHandler(svc service.Service) http.Handler
```

Status mapping:
- 200 success
- 400 `request.invalid` — bad JSON, empty text, text > 4096
- 403 `message.forbidden` — `errors.Is(err, service.ErrForbidden)`
- 404 `message.not_found` — `errors.Is(err, store.ErrNotFound)`
- 409 `wa.not_connected`
- 500 default

Response body for PATCH: same structure as POST /v1/messages response plus `edited_at`:
```json
{"id": "...", "chat_jid": "...", "ts": "...", "edited_at": "..."}
```

**DeleteMessageHandler** (new):
```go
// DELETE /v1/messages/{id}
// 204 on success.
func DeleteMessageHandler(svc service.Service) http.Handler
```

Status mapping:
- 204 success (no body)
- 403 `message.forbidden`
- 404 `message.not_found`
- 409 `wa.not_connected`
- 500 default

**Router** in `internal/transport/http/router.go` (auth-protected group):
```go
r.Method(http.MethodPatch, "/messages/{id}", EditMessageHandler(d.Service))
r.Method(http.MethodDelete, "/messages/{id}", DeleteMessageHandler(d.Service))
```

## 9. Tests

**Service** (`internal/service/service_test.go`):
- `TestSendTextWithReplyTo` — fake WA captures replyTo arg; persisted message has reply_to filled.
- `TestSendTextReplyToTooLong` → `ErrInvalidRequest`.
- `TestEditMessageHappyPath` — seed message; call EditMessage; assert SendEdit called, body + edited_at updated.
- `TestEditMessageNotFound` → `ErrNotFound`.
- `TestEditMessageForbidden` — sender_jid mismatch → `ErrForbidden`.
- `TestEditMessageOnDeleted` — deleted_at != nil → `ErrForbidden`.
- `TestEditMessageValidation` — empty newText, too long → `ErrInvalidRequest`.
- `TestDeleteMessageHappyPath` — seed, call DeleteMessage, assert SendRevoke called, SoftDelete called.
- `TestDeleteMessageNotFound`, `TestDeleteMessageForbidden`.
- `TestHandleIncomingRevoke` — IncomingMessage{RevokeOfID: "M1"} → Messages.SoftDelete called; chat unread NOT bumped.
- `TestHandleIncomingEditUpdatesBody` — IncomingMessage{EditOfID: "M1", Body: "new"} after seeding M1; verify body + edited_at updated.
- `TestHandleIncomingEditUnknownIDLogged` — EditOfID points at non-existent message; no panic, no row created.

**HTTP** (`internal/transport/http/messages_test.go` extensions):
- `TestSendTextWithReplyToField` — POST /v1/messages with `reply_to` passes through.
- `TestEditMessageHappyPath` — PATCH returns 200 + JSON with `edited_at`.
- `TestEditMessageBadRequest` — empty body, missing text → 400.
- `TestEditMessageForbidden` — fake service returns `ErrForbidden` → 403.
- `TestEditMessageNotFound` → 404.
- `TestEditMessageNotConnected` → 409.
- `TestDeleteMessageHappyPath` — DELETE returns 204.
- `TestDeleteMessageForbidden`, `TestDeleteMessageNotFound`, `TestDeleteMessageNotConnected`.

**No new mediastore or sqlite tests.** No schema changes; no new mediastore behavior.

## 10. File layout

```
internal/waclient/
  waclient.go              SendText sig change; +SendEdit, +SendRevoke; +EditOfID, +RevokeOfID
  whatsmeow_adapter.go     SendText extends ContextInfo branch; +SendEdit, +SendRevoke;
                           translateIncoming + messageKindAndBody extended for ProtocolMessage variants

internal/service/
  service.go               +ErrForbidden, +EditMessage, +DeleteMessage; SendText sig change;
                           handleIncoming routes edits/revokes BEFORE existing path
  service_test.go          new tests; fake WA gets SendEdit/SendRevoke captures

internal/transport/http/
  messages.go              SendTextHandler accepts reply_to; +EditMessageHandler, +DeleteMessageHandler
  messages_test.go         new tests
  router.go                +PATCH /messages/{id}, +DELETE /messages/{id}

README.md                  status section
```

No schema migrations. No new dependencies.

## 11. Dependencies

None added. whatsmeow already provides `Client.BuildRevoke`, `Client.SendMessage`, the `*waE2E.ProtocolMessage` proto type, and `*waE2E.ExtendedTextMessage` for replies.

## 12. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new service + HTTP tests.
- Manual smoke against a paired account:
  - Reply: `POST /v1/messages {chat_jid, text, reply_to: "<some_msg_id>"}` → recipient sees a quoted reply.
  - Edit: PATCH the just-sent message; recipient sees "edited" indicator and updated text.
  - Delete: DELETE the message; recipient sees revoke ("This message was deleted").
  - Inbound: another phone edits/deletes a message they previously sent; daemon's `messages.body` updates / `deleted_at` set.
  - Validation: PATCH with empty text → 400. DELETE someone else's message → 403. PATCH unknown ID → 404.
- Existing Plan 01–06 endpoints unchanged.

## 13. Open questions deferred to implementation

- Whether `Client.BuildEdit` exists in the installed whatsmeow version. If yes, prefer it over manually constructing the `MESSAGE_EDIT` ProtocolMessage. Run `go doc go.mau.fi/whatsmeow.Client.BuildEdit` to confirm.
- Whether the `Participant` field on `ContextInfo` is required for replies. Some WhatsApp clients render the quote even without it (the receiver's local store fills in the body); others may not. Acceptable for v1 to leave it empty; revisit if smoke testing reveals broken renders.
- Whether `EditMessage`'s ownership check should also reject messages older than WhatsApp's edit window. Current spec says we just forward; whatsmeow returns an error which we wrap as 500. A consumer who wants pre-checks can compute `time.Since(existing.Timestamp) > 15*time.Minute` themselves.

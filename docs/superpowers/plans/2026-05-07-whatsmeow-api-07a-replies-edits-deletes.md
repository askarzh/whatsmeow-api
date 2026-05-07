# whatsmeow-api Plan 07a — Replies + Edits + Deletes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three message-mutation flows: replies via `POST /v1/messages` `reply_to` field; edits via `PATCH /v1/messages/{id}`; deletes via `DELETE /v1/messages/{id}`. Inbound `events.Message` protocol-message variants (REVOKE, MESSAGE_EDIT) update local rows.

**Architecture:** waclient.SendText gains a `replyTo` parameter; new `SendEdit` and `SendRevoke` methods build the right `*waE2E.ProtocolMessage` variants. Service gets `EditMessage` and `DeleteMessage` with ownership checks against `wa.Status().JID`. HTTP routes PATCH and DELETE to a single `{id}` path param. Inbound `translateIncoming` detects ProtocolMessage and surfaces `EditOfID` / `RevokeOfID` on `IncomingMessage`; `handleIncoming` routes those before the existing Plan 04 path. No schema migrations.

**Tech Stack:**
- Go 1.26
- Plan 01–06 stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)
- whatsmeow's `Client.SendMessage`, `Client.BuildRevoke`, `*waE2E.ExtendedTextMessage`, `*waE2E.ProtocolMessage`, `*waE2E.ContextInfo`

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/waclient/waclient.go` | Modified — `SendText` sig adds `replyTo`; +`SendEdit`, +`SendRevoke` in interface; +`EditOfID`, +`RevokeOfID` on `IncomingMessage`. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — `SendText` reply branch; new `SendEdit` + `SendRevoke` impls; `translateIncoming` extended for ProtocolMessage REVOKE / MESSAGE_EDIT. |
| `internal/service/service.go` | Modified — `SendText` sig change; `+ErrForbidden`; +`EditMessage`, +`DeleteMessage`; `handleIncoming` routes revoke/edit before existing path. |
| `internal/service/service_test.go` | Modified — fake WA captures replyTo/edit/revoke; new tests. |
| `internal/transport/http/messages.go` | Modified — request struct gains `reply_to`; +`EditMessageHandler`, +`DeleteMessageHandler`. |
| `internal/transport/http/messages_test.go` | Modified — new tests. |
| `internal/transport/http/router.go` | Modified — +PATCH `/messages/{id}`, +DELETE `/messages/{id}`. |
| `cmd/whatsmeow-api/serve.go` | Unchanged. |
| Various existing HTTP fake services | Modified — already have stubs for old Service surface; the SendText sig change requires updating the existing fakeSendSvc + others to match the new 4-arg signature. |
| `README.md` | Modified — status section. |

---

## Task 1: waclient interface extension + adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service_test.go` (fakeWA stubs)

This task is the breaking-signature ripple. SendText gains a `replyTo` parameter; new `SendEdit` and `SendRevoke` methods are added to the interface; `IncomingMessage` gets two new fields. Adapter gets stub implementations of the new methods so the interface check still passes; Tasks 2, 3, 4 fill them in.

- [ ] **Step 1: Update the interface**

Edit `internal/waclient/waclient.go`. Find the `IncomingMessage` struct and append two fields:
```go
type IncomingMessage struct {
	// existing fields (Plan 04 + 06)
	ID        string
	ChatJID   string
	ChatKind  string
	SenderJID string
	Timestamp time.Time
	Kind      string
	Body      string
	PushName  string
	MediaDownloader func(ctx context.Context) ([]byte, string, error)

	// Plan 07a — set when this event is an edit or revoke of a previous message.
	// Mutually exclusive (ProtocolMessage is one variant or the other).
	EditOfID   string
	RevokeOfID string
}
```

Find the `WAClient` interface. Change `SendText` signature and add two new methods:
```go
type WAClient interface {
	// existing surface (Status, Resume, LoginQR, LoginPhone, Logout, Close, OnIncomingMessage, SendMedia)

	// CHANGED in Plan 07a: replyTo parameter added (empty string = not a reply).
	SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error)

	// NEW in Plan 07a
	SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error)
	SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error)
}
```

- [ ] **Step 2: Update SendText stub call site in adapter**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the existing `SendText` method (Plan 04). Change its signature:
```go
func (a *Adapter) SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error) {
```

The body stays unchanged for now (it ignores `replyTo`); Task 2 adds the reply branch.

Add `_ = replyTo` to the body (just inside the function) to silence the unused-parameter lint until Task 2:
```go
func (a *Adapter) SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error) {
	_ = replyTo // wired in Plan 07a Task 2
	// ... existing body unchanged ...
}
```

- [ ] **Step 3: Add adapter stubs for SendEdit + SendRevoke**

Insert before the `var _ WAClient = (*Adapter)(nil)` line at the bottom:
```go
// SendEdit is implemented in Plan 07a Task 3.
func (a *Adapter) SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error) {
	_ = ctx; _ = chatJID; _ = originalMessageID; _ = newText
	return Sent{}, errors.New("waclient: SendEdit not yet implemented")
}

// SendRevoke is implemented in Plan 07a Task 3.
func (a *Adapter) SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error) {
	_ = ctx; _ = chatJID; _ = originalMessageID
	return Sent{}, errors.New("waclient: SendRevoke not yet implemented")
}
```

Add `"errors"` to the import block if not already present.

- [ ] **Step 4: Bridge the fakeWA in service_test.go**

Edit `internal/service/service_test.go`. Find `fakeWA.SendText`:
```go
func (f *fakeWA) SendText(context.Context, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
```

Replace with the new 4-arg signature:
```go
func (f *fakeWA) SendText(context.Context, string, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
```

Add new fakeWA stubs for SendEdit and SendRevoke (after the existing methods):
```go
func (f *fakeWA) SendEdit(context.Context, string, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) SendRevoke(context.Context, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
```

Find `sendableFakeWA.SendText`:
```go
func (f *sendableFakeWA) SendText(_ context.Context, chatJID, text string) (waclient.Sent, error) {
	f.calledSend = true
	f.sentArgs[0] = chatJID
	f.sentArgs[1] = text
	return f.sendResp, f.sendErr
}
```

Replace with the new signature (capture replyTo too):
```go
func (f *sendableFakeWA) SendText(_ context.Context, chatJID, text, replyTo string) (waclient.Sent, error) {
	f.calledSend = true
	f.sentArgs[0] = chatJID
	f.sentArgs[1] = text
	f.sentArgs[2] = replyTo
	return f.sendResp, f.sendErr
}
```

(The existing `sentArgs [3]string` already has room for the third slot which Plan 04 used as "sender" — repurpose it for `replyTo` and update any test that read `sentArgs[2]` to expect "" by default.)

Verify: the only existing test reading `sentArgs[2]` is `TestSendTextSuccess` from Plan 04 — it doesn't read sentArgs[2], it reads `got.SenderJID` from the returned message. Safe to repurpose.

- [ ] **Step 5: Run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: failures in `internal/service/...` because `service.SendText` still calls `wa.SendText(ctx, chatJID, text)` (3 args) but the interface now requires 4. The next steps fix this.

Also failures in `internal/transport/http/...` because the existing fake services (fakeSendSvc, fakeChatsSvc, etc.) implement `service.Service` whose SendText signature hasn't changed yet — so the build there is fine. The break is at the waclient/service boundary.

- [ ] **Step 6: Update service.SendText to pass replyTo through**

Edit `internal/service/service.go`. Find `(*svc).SendText`:
```go
func (s *svc) SendText(ctx context.Context, chatJID, text string) (store.Message, error) {
```

Change to:
```go
func (s *svc) SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error) {
```

Inside the body, find:
```go
sent, err := s.wa.SendText(ctx, chatJID, text)
```

Replace with:
```go
sent, err := s.wa.SendText(ctx, chatJID, text, replyTo)
```

Also: persist `replyTo` in the local row. Find the `msg := store.Message{...}` block and add:
```go
msg := store.Message{
	ID:        sent.ID,
	ChatJID:   chatJID,
	SenderJID: sent.SenderJID,
	Timestamp: sent.Timestamp,
	Kind:      "text",
	Body:      text,
	ReplyTo:   replyTo, // Plan 07a
}
```

Update the Service interface signature:
```go
SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error)
```

- [ ] **Step 7: Update Service interface impls (HTTP fake services)**

The HTTP test fakes all have `func (f X) SendText(context.Context, string, string) (store.Message, error)` from Plan 04. Update each to the 4-arg signature. Files:
- `internal/transport/http/status_test.go`
- `internal/transport/http/login_qr_test.go`
- `internal/transport/http/login_phone_test.go`
- `internal/transport/http/logout_test.go`
- `internal/transport/http/messages_test.go` (the fakeSendSvc — keep its capture fields working)
- `internal/transport/http/chats_test.go`
- `internal/transport/http/contacts_test.go`
- `internal/transport/http/stats_test.go`
- `internal/transport/http/media_test.go`

For 8 of the 9 (everything except messages_test.go's fakeSendSvc): change signature only:
```go
func (f X) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
```

For `messages_test.go`'s `fakeSendSvc` — it captures arguments. Update it to also capture replyTo:
```go
type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat    string
	gotText    string
	gotReplyTo string // Plan 07a

	// Plan 05 search capture
	searchResp     []store.Message
	searchErr      error
	gotSearchQ     string
	gotSearchLimit int
}

func (f *fakeSendSvc) SendText(_ context.Context, chat, text, replyTo string) (store.Message, error) {
	f.gotChat = chat
	f.gotText = text
	f.gotReplyTo = replyTo
	return f.resp, f.err
}
```

- [ ] **Step 8: Update SendTextHandler to pass reply_to through**

Edit `internal/transport/http/messages.go`. Find `sendTextRequest`:
```go
type sendTextRequest struct {
	ChatJID string `json:"chat_jid"`
	Text    string `json:"text"`
}
```

Add `ReplyTo`:
```go
type sendTextRequest struct {
	ChatJID string `json:"chat_jid"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
}
```

Find the handler call to `svc.SendText`:
```go
msg, err := svc.SendText(r.Context(), req.ChatJID, req.Text)
```

Replace with:
```go
msg, err := svc.SendText(r.Context(), req.ChatJID, req.Text, req.ReplyTo)
```

- [ ] **Step 9: Run tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. Existing tests pass (replyTo is "" everywhere they didn't set it).

- [ ] **Step 10: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/whatsmeow_adapter.go internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "waclient+service+http: extend interfaces for replyTo + SendEdit + SendRevoke (stubs)"
```

---

## Task 2: waclient adapter SendText reply branch

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test (real WhatsApp). Manual smoke (Task 9) covers it.

- [ ] **Step 1: Replace the SendText body**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the existing SendText (Plan 04 body kept by Task 1). The current body builds `&waE2E.Message{Conversation: proto.String(text)}`. Extend it to support replies:

```go
func (a *Adapter) SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error) {
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

	var msg *waE2E.Message
	if replyTo == "" {
		msg = &waE2E.Message{Conversation: proto.String(text)}
	} else {
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String(text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String(replyTo),
				},
			},
		}
	}

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send text: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}
```

Remove the `_ = replyTo` line from Task 1 (now used).

- [ ] **Step 2: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: SendText reply branch via ExtendedTextMessage.ContextInfo"
```

---

## Task 3: waclient adapter SendEdit + SendRevoke

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test. Manual smoke (Task 9) covers it.

- [ ] **Step 1: Inspect whatsmeow APIs**

```bash
go doc go.mau.fi/whatsmeow.Client.BuildRevoke
go doc go.mau.fi/whatsmeow.Client.BuildEdit
go doc go.mau.fi/whatsmeow/proto/waE2E.ProtocolMessage
go doc go.mau.fi/whatsmeow/proto/waE2E.ProtocolMessage_REVOKE
go doc go.mau.fi/whatsmeow/proto/waE2E.ProtocolMessage_MESSAGE_EDIT
```

Confirm:
- `Client.BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message` — likely exists.
- `Client.BuildEdit(...)` — may or may not exist; if absent, build manually as shown below.
- `*waE2E.ProtocolMessage` has `Type *Type`, `Key *MessageKey`, `EditedMessage *Message` fields.

If signatures differ, adapt — the intent is documented in step 2.

- [ ] **Step 2: Replace the SendEdit + SendRevoke stubs**

Replace the two stubs from Task 1 with real implementations:

```go
// SendEdit sends a MESSAGE_EDIT ProtocolMessage targeting the given message id.
// Only owner-edits succeed (whatsmeow rejects edits to messages we didn't send).
func (a *Adapter) SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error) {
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

	editType := waE2E.ProtocolMessage_MESSAGE_EDIT
	msg := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: &editType,
			Key: &waCommon.MessageKey{
				FromMe:    proto.Bool(true),
				ID:        proto.String(originalMessageID),
				RemoteJID: proto.String(chatJID),
			},
			EditedMessage: &waE2E.Message{Conversation: proto.String(newText)},
			TimestampMS:   proto.Int64(time.Now().UnixMilli()),
		},
	}

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send edit: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}

// SendRevoke sends a REVOKE ProtocolMessage targeting the given message id.
func (a *Adapter) SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	senderTypes := a.client.Store.ID
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}

	// Prefer Client.BuildRevoke if available — it sets all the right key fields.
	msg := client.BuildRevoke(to, *senderTypes, originalMessageID)

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send revoke: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}
```

Add `"go.mau.fi/whatsmeow/proto/waCommon"` to the import block. The other imports (`types`, `waE2E`, `proto`, `time`, `fmt`) should already be present from Plan 04 + 06.

> **Note:** if `client.BuildRevoke` has a different signature (e.g. doesn't dereference senderJID), adapt. If it doesn't exist at all, build the revoke message manually like SendEdit does, with `Type: ProtocolMessage_REVOKE` and just the Key (no EditedMessage).

- [ ] **Step 3: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: implement SendEdit + SendRevoke via ProtocolMessage"
```

---

## Task 4: waclient adapter translateIncoming protocol message handling

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

Extends `translateIncoming` so inbound REVOKE / MESSAGE_EDIT events become `IncomingMessage` with `RevokeOfID` / `EditOfID` set. No automated test; manual smoke (Task 9) covers.

- [ ] **Step 1: Update translateIncoming**

Edit `internal/waclient/whatsmeow_adapter.go`. The current `translateIncoming` (extended by Plan 06 for media) calls `messageKindAndBody` and returns. Add a ProtocolMessage branch BEFORE the kind/body call:

```go
func translateIncoming(a *Adapter, evt *events.Message) (IncomingMessage, bool) {
	if evt.Info.IsFromMe {
		return IncomingMessage{}, false
	}
	if evt.Message != nil && evt.Message.ProtocolMessage != nil {
		return translateProtocol(evt)
	}
	kind, body, downloader, ok := messageKindAndBody(a, evt.Message)
	if !ok {
		return IncomingMessage{}, false
	}
	return IncomingMessage{
		ID:              evt.Info.ID,
		ChatJID:         evt.Info.Chat.String(),
		ChatKind:        ChatKindFromJID(evt.Info.Chat.String()),
		SenderJID:       evt.Info.Sender.String(),
		Timestamp:       evt.Info.Timestamp,
		Kind:            kind,
		Body:            body,
		PushName:        evt.Info.PushName,
		MediaDownloader: downloader,
	}, true
}

// translateProtocol handles inbound *waE2E.ProtocolMessage events for revoke +
// edit. Returns false for protocol-message types we don't handle in Plan 07a
// (e.g. read receipts arrive separately).
func translateProtocol(evt *events.Message) (IncomingMessage, bool) {
	pm := evt.Message.ProtocolMessage
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
		body := ""
		if edited := pm.GetEditedMessage(); edited != nil {
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
		return IncomingMessage{}, false
	}
}
```

- [ ] **Step 2: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. (Service tests don't exercise ProtocolMessage paths yet — Task 6 adds those.)

- [ ] **Step 3: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: translateIncoming detects REVOKE + MESSAGE_EDIT"
```

---

## Task 5: service EditMessage + DeleteMessage

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestEditMessageHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	myJID := jid
	wa := &editFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}},
		editResp: waclient.Sent{ID: "EDIT1", Timestamp: time.Unix(2000, 0).UTC(), SenderJID: jid},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: jid,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "old",
	}

	got, err := s.EditMessage(ctx, "M1", "new text")
	require.NoError(t, err)
	assert.Equal(t, "new text", got.Body)
	require.NotNil(t, got.EditedAt)
	assert.True(t, got.EditedAt.Equal(time.Unix(2000, 0).UTC()))

	assert.Equal(t, "M1", wa.gotEditMessageID)
	assert.Equal(t, "new text", wa.gotEditNewText)
	assert.Equal(t, "c@s.whatsapp.net", wa.gotEditChatJID)
}

func TestEditMessageNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.EditMessage(context.Background(), "missing", "x")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestEditMessageForbiddenWrongSender(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "someone-else@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	_, err := s.EditMessage(context.Background(), "M1", "new")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}

func TestEditMessageForbiddenAlreadyDeleted(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	deletedAt := time.Unix(1500, 0).UTC()
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: myJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
		DeletedAt: &deletedAt,
	}
	_, err := s.EditMessage(context.Background(), "M1", "new")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}

func TestEditMessageValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.EditMessage(context.Background(), "", "text")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	_, err = s.EditMessage(context.Background(), "M1", "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	_, err = s.EditMessage(context.Background(), "M1", strings.Repeat("x", 4097))
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestDeleteMessageHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &editFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: jid,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	require.NoError(t, s.DeleteMessage(ctx, "M1"))
	assert.Equal(t, "M1", wa.gotRevokeMessageID)
	assert.Equal(t, "c@s.whatsapp.net", wa.gotRevokeChatJID)
}

func TestDeleteMessageNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.DeleteMessage(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestDeleteMessageForbidden(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.DeleteMessage(context.Background(), "M1")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}
```

Add the supporting fake type at the same location as `mediaSenderFakeWA`:
```go
type editFakeWA struct {
	fakeWA
	editResp           waclient.Sent
	editErr            error
	gotEditChatJID     string
	gotEditMessageID   string
	gotEditNewText     string
	revokeResp         waclient.Sent
	revokeErr          error
	gotRevokeChatJID   string
	gotRevokeMessageID string
}

func (f *editFakeWA) SendEdit(_ context.Context, chatJID, messageID, newText string) (waclient.Sent, error) {
	f.gotEditChatJID = chatJID
	f.gotEditMessageID = messageID
	f.gotEditNewText = newText
	return f.editResp, f.editErr
}
func (f *editFakeWA) SendRevoke(_ context.Context, chatJID, messageID string) (waclient.Sent, error) {
	f.gotRevokeChatJID = chatJID
	f.gotRevokeMessageID = messageID
	return f.revokeResp, f.revokeErr
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestEditMessage|TestDeleteMessage'
```

Expected: FAIL — `service.ErrForbidden`, `(*svc).EditMessage`, `(*svc).DeleteMessage` undefined.

- [ ] **Step 3: Implement Service additions**

Edit `internal/service/service.go`. Add the new sentinel near `ErrInvalidRequest`:
```go
var ErrForbidden = errors.New("service: forbidden")
```

Extend the Service interface:
```go
EditMessage(ctx context.Context, messageID, newText string) (store.Message, error)
DeleteMessage(ctx context.Context, messageID string) error
```

Append the methods at the bottom:
```go
func (s *svc) EditMessage(ctx context.Context, messageID, newText string) (store.Message, error) {
	if strings.TrimSpace(messageID) == "" {
		return store.Message{}, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	if newText == "" {
		return store.Message{}, fmt.Errorf("%w: text is required", ErrInvalidRequest)
	}
	if len(newText) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return store.Message{}, err
	}
	if !s.ownsMessage(existing) {
		return store.Message{}, fmt.Errorf("%w: not the message sender", ErrForbidden)
	}
	if existing.DeletedAt != nil {
		return store.Message{}, fmt.Errorf("%w: message is deleted", ErrForbidden)
	}

	sent, err := s.wa.SendEdit(ctx, existing.ChatJID, messageID, newText)
	if err != nil {
		return store.Message{}, err
	}

	existing.Body = newText
	t := sent.Timestamp
	existing.EditedAt = &t
	if err := s.bundle.Messages.Put(ctx, existing); err != nil {
		s.logger.Warn("persist edit failed", "id", messageID, "err", err)
	}
	return existing, nil
}

func (s *svc) DeleteMessage(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}
	if !s.ownsMessage(existing) {
		return fmt.Errorf("%w: not the message sender", ErrForbidden)
	}
	if existing.DeletedAt != nil {
		return fmt.Errorf("%w: already deleted", ErrForbidden)
	}

	if _, err := s.wa.SendRevoke(ctx, existing.ChatJID, messageID); err != nil {
		return err
	}

	if err := s.bundle.Messages.SoftDelete(ctx, messageID, time.Now()); err != nil {
		s.logger.Warn("soft-delete after revoke failed", "id", messageID, "err", err)
	}
	return nil
}

// ownsMessage reports whether the daemon's current JID matches the message's
// sender JID. Returns false if the daemon isn't currently logged in.
func (s *svc) ownsMessage(m store.Message) bool {
	st := s.wa.Status()
	if st.JID == nil {
		return false
	}
	return *st.JID == m.SenderJID
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — all 8 new edit/delete tests + existing tests.

- [ ] **Step 5: Bridge HTTP fakes for EditMessage + DeleteMessage**

The HTTP fake services have `var _ service.Service = ...` checks. Add stubs for the two new methods to each fake (status, login_qr, login_phone, logout, messages, chats, contacts, stats, media — 9 files):

For each fake service, append:
```go
func (f X) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f X) DeleteMessage(context.Context, string) error { return nil }
```

(Adapt `f X` to the receiver style of each fake — value or pointer.)

For `messages_test.go`'s `fakeSendSvc`, you can extend it with capture fields if needed for Task 8; otherwise just stub for now.

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "service: EditMessage + DeleteMessage with ownership checks"
```

---

## Task 6: service handleIncoming routes for revoke + edit

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestHandleIncomingRevoke(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	// Seed the message that gets revoked.
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "secret",
	}

	wa.incoming(waclient.IncomingMessage{
		ID:         "EVT1",
		ChatJID:    "c@s.whatsapp.net",
		ChatKind:   "user",
		SenderJID:  "other@s.whatsapp.net",
		Timestamp:  time.Unix(2000, 0).UTC(),
		RevokeOfID: "M1",
	})

	got, err := bundle.Messages.Get(context.Background(), "M1")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
}

func TestHandleIncomingEditUpdatesBody(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "original",
	}

	editTS := time.Unix(2000, 0).UTC()
	wa.incoming(waclient.IncomingMessage{
		ID:        "EVT2",
		ChatJID:   "c@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "other@s.whatsapp.net",
		Timestamp: editTS,
		Body:      "edited body",
		EditOfID:  "M1",
	})

	got, err := bundle.Messages.Get(context.Background(), "M1")
	require.NoError(t, err)
	assert.Equal(t, "edited body", got.Body)
	require.NotNil(t, got.EditedAt)
	assert.True(t, got.EditedAt.Equal(editTS))
}

func TestHandleIncomingEditUnknownIDLogged(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	// No seeded message; the edit references a non-existent ID.
	wa.incoming(waclient.IncomingMessage{
		ID:        "EVT3",
		ChatJID:   "c@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Body:      "edited body",
		EditOfID:  "NON_EXISTENT",
	})

	// No row should be created.
	_, err := bundle.Messages.Get(context.Background(), "NON_EXISTENT")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestHandleIncomingRevokeDoesNotBumpUnread(t *testing.T) {
	bundle, chats, msgs, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "other@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		RevokeOfID: "M1",
	})

	chat, err := bundle.Chats.Get(context.Background(), "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 5, chat.UnreadCount, "revoke must not bump unread_count")
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run TestHandleIncoming
```

Expected: FAIL — handleIncoming doesn't yet route revoke/edit; revoke test sees `DeletedAt == nil`.

- [ ] **Step 3: Update handleIncoming**

Edit `internal/service/service.go`. Find `func (s *svc) handleIncoming(...)`. Insert at the very top (BEFORE the existing contact/chat/message persistence):

```go
func (s *svc) handleIncoming(msg waclient.IncomingMessage) {
	ctx := context.Background()

	// Plan 07a: route edits and revokes BEFORE the normal-message path.
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

	// existing Plan 04 + Plan 06 path follows ...
```

Make sure the existing contact / chat / message persist code stays after these new branches.

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — 4 new tests + all existing.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: handleIncoming routes edits + revokes before normal path"
```

---

## Task 7: HTTP EditMessageHandler + DeleteMessageHandler + reply_to passthrough

**Files:**
- Modify: `internal/transport/http/messages.go`
- Modify: `internal/transport/http/messages_test.go`
- Modify: `internal/transport/http/router.go`

The `reply_to` field on POST /v1/messages was already added in Task 1 (signature wiring). This task adds tests for it plus the two new mutation handlers.

- [ ] **Step 1: Add the failing tests**

Append to `internal/transport/http/messages_test.go`:
```go
func TestSendTextWithReplyToField(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeSendSvc{resp: store.Message{
		ID: "MID1", ChatJID: "c@s.whatsapp.net", Timestamp: now, Kind: "text", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendTextHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"chat_jid":"c@s.whatsapp.net","text":"hi","reply_to":"PARENT_ID"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, "PARENT_ID", f.gotReplyTo)
}

func TestEditMessageHappyPath(t *testing.T) {
	editedAt := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeSendSvc{editResp: store.Message{
		ID: "MID1", ChatJID: "c@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind: "text", Body: "new", EditedAt: &editedAt,
	}}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "MID1", f.gotEditID)
	assert.Equal(t, "new", f.gotEditText)
}

func TestEditMessageEmptyText(t *testing.T) {
	f := &fakeSendSvc{}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":""}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestEditMessageForbidden(t *testing.T) {
	f := &fakeSendSvc{editErr: service.ErrForbidden}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestEditMessageNotFound(t *testing.T) {
	f := &fakeSendSvc{editErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestEditMessageNotConnected(t *testing.T) {
	f := &fakeSendSvc{editErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Patch("/v1/messages/{id}", httpapi.EditMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"text":"new"}`)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/messages/MID1", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestDeleteMessageHappyPath(t *testing.T) {
	f := &fakeSendSvc{}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotDeleteID)
}

func TestDeleteMessageForbidden(t *testing.T) {
	f := &fakeSendSvc{deleteErr: service.ErrForbidden}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestDeleteMessageNotFound(t *testing.T) {
	f := &fakeSendSvc{deleteErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestDeleteMessageNotConnected(t *testing.T) {
	f := &fakeSendSvc{deleteErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Delete("/v1/messages/{id}", httpapi.DeleteMessageHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/MID1", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}
```

Update `fakeSendSvc` (in the same file) to capture edit + delete args:
```go
type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat    string
	gotText    string
	gotReplyTo string

	editResp store.Message
	editErr  error
	gotEditID   string
	gotEditText string

	deleteErr   error
	gotDeleteID string

	searchResp     []store.Message
	searchErr      error
	gotSearchQ     string
	gotSearchLimit int
}

func (f *fakeSendSvc) EditMessage(_ context.Context, id, text string) (store.Message, error) {
	f.gotEditID = id
	f.gotEditText = text
	return f.editResp, f.editErr
}
func (f *fakeSendSvc) DeleteMessage(_ context.Context, id string) error {
	f.gotDeleteID = id
	return f.deleteErr
}
```

(Replace the no-op stubs from Task 5 with these captures.)

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/transport/http/... -run 'TestEditMessage|TestDeleteMessage|TestSendTextWithReplyTo'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Edit `internal/transport/http/messages.go`. Append:
```go
type editMessageRequest struct {
	Text string `json:"text"`
}

// EditMessageHandler handles PATCH /v1/messages/{id}: edit an outbound text
// message. Body: {"text": "..."}.
func EditMessageHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		var req editMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		if req.Text == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "text is required")
			return
		}

		msg, err := svc.EditMessage(r.Context(), messageID, req.Text)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			case errors.Is(err, service.ErrForbidden):
				WriteProblem(w, http.StatusForbidden, "message.forbidden", err.Error())
			case errors.Is(err, store.ErrNotFound):
				WriteProblem(w, http.StatusNotFound, "message.not_found", err.Error())
			case errors.Is(err, waclient.ErrNotConnected):
				WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
			}
			return
		}

		body := map[string]any{
			"id":       msg.ID,
			"chat_jid": msg.ChatJID,
			"ts":       msg.Timestamp.UTC().Format(time.RFC3339),
		}
		if msg.EditedAt != nil {
			body["edited_at"] = msg.EditedAt.UTC().Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, body)
	})
}

// DeleteMessageHandler handles DELETE /v1/messages/{id}: revoke an outbound
// message and soft-delete the local row.
func DeleteMessageHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		err := svc.DeleteMessage(r.Context(), messageID)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, service.ErrForbidden):
			WriteProblem(w, http.StatusForbidden, "message.forbidden", err.Error())
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "message.not_found", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}
```

Add `chi/v5` and `store` to imports if not already present. (Plan 04 already has `service`, `waclient`, `errors`, `time`, `json`. `chi/v5` is in chats.go.)

- [ ] **Step 4: Wire the routes**

Edit `internal/transport/http/router.go`. In the auth-protected group, append:
```go
r.Method(http.MethodPatch, "/messages/{id}", EditMessageHandler(d.Service))
r.Method(http.MethodDelete, "/messages/{id}", DeleteMessageHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/messages.go internal/transport/http/messages_test.go internal/transport/http/router.go
git commit -m "http: PATCH + DELETE /v1/messages/{id}; SendText reply_to wired"
```

---

## Task 8: End-to-end smoke test

**Files:** none modified.

Verify the validation paths work; optionally exercise real flows against a paired account.

- [ ] **Step 1: Build and start daemon**

```bash
pkill -f "whatsmeow-api serve" 2>/dev/null; sleep 1
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
```

Expected: `app store opened`, `server starting`.

- [ ] **Step 2: Validation paths**

```bash
# Empty text on PATCH → 400
curl -i -X PATCH -H "Content-Type: application/json" \
  -d '{"text":""}' \
  http://127.0.0.1:8080/v1/messages/MID1

# Unknown id, daemon paired check would matter but no auth + no message → 404
curl -i -X PATCH -H "Content-Type: application/json" \
  -d '{"text":"new"}' \
  http://127.0.0.1:8080/v1/messages/MID1

# Same for DELETE
curl -i -X DELETE http://127.0.0.1:8080/v1/messages/MID1
```

Expected (with empty DB and unpaired daemon):
- Empty text → 400 `request.invalid`.
- Unknown id PATCH → 404 `message.not_found`.
- Unknown id DELETE → 404 `message.not_found`.

- [ ] **Step 3: Reply via POST**

```bash
curl -i -X POST -H "Content-Type: application/json" \
  -d '{"chat_jid":"+27821234567@s.whatsapp.net","text":"hi","reply_to":"FAKE_PARENT"}' \
  http://127.0.0.1:8080/v1/messages
```

Expected: 409 (not connected). The reply_to field passed validation.

- [ ] **Step 4: (Optional) Real round-trip with a paired account**

If you've paired via `./bin/whatsmeow-api login qr`:

```bash
# Send a message
JID="<YOUR_JID>"
curl -X POST -H "Content-Type: application/json" \
  -d "{\"chat_jid\":\"$JID\",\"text\":\"plan 07a hello\"}" \
  http://127.0.0.1:8080/v1/messages
# → returns {"id":"3EB05...","chat_jid":"...","ts":"..."}

# Reply to it (use the id from the response above)
PARENT_ID="3EB05..."
curl -X POST -H "Content-Type: application/json" \
  -d "{\"chat_jid\":\"$JID\",\"text\":\"this is a reply\",\"reply_to\":\"$PARENT_ID\"}" \
  http://127.0.0.1:8080/v1/messages
# → recipient phone shows the reply quoted

# Edit a sent message
curl -X PATCH -H "Content-Type: application/json" \
  -d '{"text":"plan 07a hello (edited)"}' \
  http://127.0.0.1:8080/v1/messages/$PARENT_ID

# Delete a sent message
NEW_ID=$(curl -s -X POST -H "Content-Type: application/json" \
  -d "{\"chat_jid\":\"$JID\",\"text\":\"to be deleted\"}" \
  http://127.0.0.1:8080/v1/messages | jq -r .id)
curl -i -X DELETE http://127.0.0.1:8080/v1/messages/$NEW_ID
# → 204; recipient phone shows revoke

# Inbound: from another phone, send a message, then edit / delete it. Wait ~3s.
sqlite3 data/whatsmeow-app.db 'SELECT id, body, deleted_at, edited_at FROM messages ORDER BY ts DESC LIMIT 5'
# → daemon's local row reflects the edit/delete
```

- [ ] **Step 5: Stop daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -3 /tmp/wmapi.log
```

Expected: `... msg="server stopped"`.

- [ ] **Step 6: No commit**

---

## Task 9: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status section**

Edit `README.md`. Replace the existing Status block (find the lines after Plan 06):
```markdown
- **Plan 06 (media)** shipped: `POST /v1/media` (multipart/form-data) sends image + document outbound; `GET /v1/media/{message_id}` streams stored bytes; inbound media events auto-download in a background goroutine for all 5 kinds (image, video, audio, document, sticker). Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>` (content-addressable). Body cap configurable via `[http] max_body_bytes` (default 100 MiB).

Reactions / replies / edits / deletes / read receipts land in Plan 07; SSE event stream in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

…with:
```markdown
- **Plan 06 (media)** shipped: `POST /v1/media` (multipart/form-data) sends image + document outbound; `GET /v1/media/{message_id}` streams stored bytes; inbound media events auto-download in a background goroutine for all 5 kinds (image, video, audio, document, sticker). Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>` (content-addressable). Body cap configurable via `[http] max_body_bytes` (default 100 MiB).
- **Plan 07a (replies + edits + deletes)** shipped: `POST /v1/messages` accepts `reply_to`; `PATCH /v1/messages/{id}` edits an outbound message (owner-only, 403 otherwise); `DELETE /v1/messages/{id}` revokes via whatsmeow's REVOKE ProtocolMessage. Inbound REVOKE / MESSAGE_EDIT events from whatsmeow update local rows (`deleted_at`, `body` + `edited_at`).

Reactions land in Plan 07b; read receipts + typing in Plan 07c; SSE event stream in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 07a"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS
- [ ] Manual smoke (Task 8 Steps 1-3): validation 400s; 404 for unknown ids; 409 for unpaired
- [ ] (Optional with paired account) Task 8 Step 4: reply / edit / delete round-trip; inbound revoke/edit reflected locally
- [ ] `git log --oneline` shows ~9 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 07b — reactions** (sibling).

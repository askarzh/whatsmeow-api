# whatsmeow-api Plan 07c — Read Receipts + Typing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three new endpoints (POST `/v1/messages/{id}/read`, POST `/v1/chats/{jid}/typing`, GET `/v1/messages/{id}/receipts`) plus a new `receipts` table populated from inbound `events.Receipt`.

**Architecture:** New `0003_receipts` migration. New `ReceiptStore` interface joins `Bundle`. `WAClient` gains `MarkRead`, `SendChatPresence`, `OnIncomingReceipt`; adapter implements them and routes `*events.Receipt` to a separate callback (distinct from `OnIncomingMessage` because receipts arrive as their own whatsmeow event type). Service composes the outbound flows + persists inbound receipts.

**Tech Stack:**
- Go 1.26
- Plan 01–07b stack
- whatsmeow's `Client.MarkRead`, `Client.SendChatPresence`, `events.Receipt`, `types.ChatPresence`

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/store.go` | Modified — `+Receipt`, `+ReceiptStore`, `+Bundle.Receipts`. |
| `internal/store/migrations/sqlite/0003_receipts.up.sql` | NEW. |
| `internal/store/migrations/sqlite/0003_receipts.down.sql` | NEW. |
| `internal/store/sqlite/receipts.go` | NEW. |
| `internal/store/sqlite/receipts_test.go` | NEW. |
| `internal/store/sqlite/store.go` | Modified — wire `*ReceiptStore`. |
| `internal/waclient/waclient.go` | Modified — `+MarkRead`, `+SendChatPresence`, `+OnIncomingReceipt`, `+IncomingReceipt`. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — impls + onEvent receipt branch + translateReceipt. |
| `internal/service/service.go` | Modified — `+MarkMessageRead`, `+SendTyping`, `+ListReceipts`, `+handleReceipt`; `New` registers `OnIncomingReceipt`. |
| `internal/service/service_test.go` | Modified — `+receiptStore` fake; bundle helper returns 6 values; new tests; bridge fakes. |
| `internal/transport/http/receipts.go` | NEW — `MarkReadHandler`, `ListReceiptsHandler`. |
| `internal/transport/http/receipts_test.go` | NEW. |
| `internal/transport/http/typing.go` | NEW — `SendTypingHandler`. |
| `internal/transport/http/typing_test.go` | NEW. |
| `internal/transport/http/router.go` | Modified — +3 routes. |
| Existing HTTP fakes (9 files) | Modified — bridge stubs for new Service surface. |
| `README.md` | Modified — status section. |

---

## Task 1: Migration + Receipt type + ReceiptStore + Bundle field

**Files:**
- Create: `internal/store/migrations/sqlite/0003_receipts.up.sql`
- Create: `internal/store/migrations/sqlite/0003_receipts.down.sql`
- Modify: `internal/store/store.go`

- [ ] **Step 1: Up migration**

`internal/store/migrations/sqlite/0003_receipts.up.sql`:
```sql
CREATE TABLE receipts (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reader_jid TEXT NOT NULL,
    type       TEXT NOT NULL,
    ts         INTEGER NOT NULL,
    PRIMARY KEY (message_id, reader_jid, type)
);
CREATE INDEX idx_receipts_message ON receipts(message_id);
```

- [ ] **Step 2: Down migration**

`internal/store/migrations/sqlite/0003_receipts.down.sql`:
```sql
DROP INDEX IF EXISTS idx_receipts_message;
DROP TABLE IF EXISTS receipts;
```

- [ ] **Step 3: Extend store.go**

Edit `internal/store/store.go`. Append to the domain types:
```go
// Receipt is one inbound acknowledgement of one of our outbound messages.
// PK is (MessageID, ReaderJID, Type) — same reader can have separate
// "delivered" → "read" → "played" rows.
type Receipt struct {
	MessageID string
	ReaderJID string
	Type      string // "delivered" | "read" | "played"
	Timestamp time.Time
}
```

Add the interface near the others:
```go
// ReceiptStore manages the receipts table.
type ReceiptStore interface {
	Put(ctx context.Context, r Receipt) error
	ListByMessageID(ctx context.Context, messageID string) ([]Receipt, error)
}
```

Extend `Bundle`:
```go
type Bundle struct {
	Chats     ChatStore
	Messages  MessageStore
	Contacts  ContactStore
	Media     MediaStore
	Events    EventsLog
	KV        KV
	Reactions ReactionStore
	Receipts  ReceiptStore // Plan 07c
}
```

- [ ] **Step 4: Build**

```bash
cd /home/askar/src/whatsmeow-api
go build ./...
go vet ./...
```

Expected: clean. The new `Bundle.Receipts` is a nil interface field for now — Tasks 2/wire fill it.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/migrations/sqlite/0003_receipts.up.sql internal/store/migrations/sqlite/0003_receipts.down.sql
git commit -m "store: add 0003_receipts migration + ReceiptStore interface"
```

---

## Task 2: SQLite ReceiptStore impl + bundle wire + extend in-memory bundle helper

**Files:**
- Create: `internal/store/sqlite/receipts.go`
- Create: `internal/store/sqlite/receipts_test.go`
- Modify: `internal/store/sqlite/store.go`
- Modify: `internal/store/sqlite/store_test.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/sqlite/receipts_test.go`:
```go
package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMessageForReceipts(t *testing.T, b store.Bundle, chatJID, messageID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Chats.Put(ctx, store.Chat{JID: chatJID, Kind: "user"}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: messageID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}))
}

func TestReceiptPutGetList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	b := s.Bundle()
	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")

	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "delivered", Timestamp: now,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: now,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "bob@s.whatsapp.net", Type: "delivered", Timestamp: now,
	}))

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Sorted by reader_jid ASC, then type ASC.
	assert.Equal(t, "alice@s.whatsapp.net", got[0].ReaderJID)
	assert.Equal(t, "delivered", got[0].Type)
	assert.Equal(t, "alice@s.whatsapp.net", got[1].ReaderJID)
	assert.Equal(t, "read", got[1].Type)
	assert.Equal(t, "bob@s.whatsapp.net", got[2].ReaderJID)
}

func TestReceiptPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")

	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t1,
	}))
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t2,
	}))

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Timestamp.Equal(t2))
}

func TestReceiptListEmpty(t *testing.T) {
	got, err := newTestStore(t).Bundle().Receipts.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReceiptFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessageForReceipts(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: time.Now(),
	}))

	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, "M1")
	require.NoError(t, err)

	got, err := b.Receipts.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./internal/store/sqlite/... -run TestReceipt
```

Expected: FAIL — `Bundle.Receipts` is nil (Task 1 added the field but Task 2 wires the impl).

- [ ] **Step 3: Implement ReceiptStore**

Create `internal/store/sqlite/receipts.go`:
```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

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

- [ ] **Step 4: Wire into sqlite.Store**

Edit `internal/store/sqlite/store.go`. Add `receipts *ReceiptStore` to the Store struct. Construct in `New`. Add to `Bundle()`:
```go
return store.Bundle{
	Chats:     s.chats,
	Messages:  s.messages,
	Contacts:  s.contacts,
	Media:     s.media,
	Events:    s.events,
	KV:        s.kv,
	Reactions: s.reactions,
	Receipts:  s.receipts, // Plan 07c
}
```

- [ ] **Step 5: Update store_test.go**

Edit `internal/store/sqlite/store_test.go`. Add `"receipts"` to `expectedTables`:
```go
expectedTables := []string{
	"chats", "contacts", "events_log", "kv", "media", "messages", "messages_fts", "reactions", "receipts",
}
```

Add to `TestBundleFieldsNonNil`:
```go
assert.NotNil(t, b.Receipts) // Plan 07c
```

- [ ] **Step 6: Run sqlite tests**

```bash
go test ./internal/store/sqlite/... -v
```

Expected: PASS — 4 new receipt tests + existing.

- [ ] **Step 7: Extend in-memory bundle helper**

Edit `internal/service/service_test.go`. After the existing `reactionStore`, append:
```go
type receiptStore struct {
	mu sync.Mutex
	m  map[string]store.Receipt // key: messageID + "|" + readerJID + "|" + type
}

func (s *receiptStore) Put(_ context.Context, r store.Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]store.Receipt{}
	}
	s.m[r.MessageID+"|"+r.ReaderJID+"|"+r.Type] = r
	return nil
}

func (s *receiptStore) ListByMessageID(_ context.Context, messageID string) ([]store.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Receipt, 0)
	for _, r := range s.m {
		if r.MessageID == messageID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReaderJID != out[j].ReaderJID {
			return out[i].ReaderJID < out[j].ReaderJID
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}
```

Update `newInMemoryBundle` to return 6 values (the new 6th is `*receiptStore`):
```go
func newInMemoryBundle() (store.Bundle, *memChats, *memMessages, *memContacts, *reactionStore, *receiptStore) {
	c := memChats{}
	m := memMessages{}
	co := memContacts{}
	rs := &reactionStore{m: map[string]store.Reaction{}}
	rcps := &receiptStore{m: map[string]store.Receipt{}}
	return store.Bundle{
		Chats:     &chatStore{m: c},
		Messages:  &messageStore{m: m},
		Contacts:  &contactStore{m: co},
		Media:     &mediaStore{},
		Events:    &eventsStore{},
		KV:        &kvStore{m: map[string]string{}},
		Reactions: rs,
		Receipts:  rcps,
	}, &c, &m, &co, rs, rcps
}
```

Update ALL existing call sites. `grep -n "newInMemoryBundle()" internal/service/service_test.go` lists them. Each like:
```go
bundle, chats, msgs, contacts, _ := newInMemoryBundle()
```
becomes:
```go
bundle, chats, msgs, contacts, _, _ := newInMemoryBundle()
```
There are roughly 40 call sites. Update each.

- [ ] **Step 8: Run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/sqlite/receipts.go internal/store/sqlite/receipts_test.go internal/store/sqlite/store.go internal/store/sqlite/store_test.go internal/service/service_test.go
git commit -m "store/sqlite: ReceiptStore impl with FK cascade test; bundle wired"
```

---

## Task 3: waclient interface + adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service_test.go` (fakeWA stubs)

- [ ] **Step 1: Extend the interface**

Edit `internal/waclient/waclient.go`. Add the IncomingReceipt struct near IncomingMessage:
```go
// IncomingReceipt is one inbound acknowledgement event for one or more
// outbound messages. Plan 07c — separate from IncomingMessage because
// events.Receipt is a distinct whatsmeow event type.
type IncomingReceipt struct {
	MessageIDs []string
	ChatJID    string
	ReaderJID  string
	Type       string // "delivered" | "read" | "played"
	Timestamp  time.Time
}
```

Append to the WAClient interface:
```go
// Plan 07c
MarkRead(ctx context.Context, chatJID, senderJID, messageID string, timestamp time.Time) error
SendChatPresence(ctx context.Context, chatJID, state string) error
OnIncomingReceipt(handler func(IncomingReceipt))
```

- [ ] **Step 2: Add adapter incomingReceipt field + stubs**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the `Adapter` struct and add a field next to `incomingHandler`:
```go
type Adapter struct {
	// existing fields
	incomingReceipt func(IncomingReceipt) // Plan 07c
}
```

Insert before `var _ WAClient = (*Adapter)(nil)`:
```go
// MarkRead is implemented in Plan 07c Task 4.
func (a *Adapter) MarkRead(ctx context.Context, chatJID, senderJID, messageID string, timestamp time.Time) error {
	_ = ctx; _ = chatJID; _ = senderJID; _ = messageID; _ = timestamp
	return errors.New("waclient: MarkRead not yet implemented")
}

// SendChatPresence is implemented in Plan 07c Task 4.
func (a *Adapter) SendChatPresence(ctx context.Context, chatJID, state string) error {
	_ = ctx; _ = chatJID; _ = state
	return errors.New("waclient: SendChatPresence not yet implemented")
}

// OnIncomingReceipt registers a handler invoked once per inbound receipt event.
func (a *Adapter) OnIncomingReceipt(handler func(IncomingReceipt)) {
	a.mu.Lock()
	a.incomingReceipt = handler
	a.mu.Unlock()
}
```

Add `"errors"` import if not present.

- [ ] **Step 3: Bridge fakeWA**

Edit `internal/service/service_test.go`. Add to fakeWA:
```go
func (f *fakeWA) MarkRead(context.Context, string, string, string, time.Time) error {
	return nil
}
func (f *fakeWA) SendChatPresence(context.Context, string, string) error { return nil }
func (f *fakeWA) OnIncomingReceipt(h func(waclient.IncomingReceipt)) {
	f.incomingReceipt = h
}
```

Add the `incomingReceipt func(waclient.IncomingReceipt)` field to the fakeWA struct.

- [ ] **Step 4: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/whatsmeow_adapter.go internal/service/service_test.go
git commit -m "waclient: MarkRead + SendChatPresence + OnIncomingReceipt (stubs)"
```

---

## Task 4: waclient adapter MarkRead + SendChatPresence implementations

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test (real WhatsApp). Manual smoke (Task 9) covers it.

- [ ] **Step 1: Inspect whatsmeow API**

```bash
go doc go.mau.fi/whatsmeow.Client.MarkRead
go doc go.mau.fi/whatsmeow.Client.SendChatPresence
go doc go.mau.fi/whatsmeow/types.ChatPresence
go doc go.mau.fi/whatsmeow/types.ChatPresenceMedia
```

Confirm signatures. Likely:
- `MarkRead(ids []types.MessageID, timestamp time.Time, chat, sender types.JID) error`
- `SendChatPresence(jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error`
- Constants: `types.ChatPresenceComposing`, `types.ChatPresencePaused`, `types.ChatPresenceMediaText`

If signatures differ, adapt — the intent is documented in step 2.

- [ ] **Step 2: Replace the MarkRead stub**

```go
// MarkRead sends a read receipt for messageID to senderJID in chatJID.
func (a *Adapter) MarkRead(ctx context.Context, chatJID, senderJID, messageID string, timestamp time.Time) error {
	_ = ctx
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

- [ ] **Step 3: Replace the SendChatPresence stub**

```go
// SendChatPresence sends typing or paused presence to chatJID.
// state must be "composing" or "paused".
func (a *Adapter) SendChatPresence(ctx context.Context, chatJID, state string) error {
	_ = ctx
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

Remove `"errors"` import if it's no longer used after the stubs are replaced.

- [ ] **Step 4: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: implement MarkRead + SendChatPresence"
```

---

## Task 5: waclient onEvent receipt branch + translateReceipt

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

- [ ] **Step 1: Add the events.Receipt case**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the `onEvent` function. Add a new case after the existing `*events.Message` case:
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

- [ ] **Step 2: Add translateReceipt helper**

Append after `translateReaction` (or wherever the other translate helpers live):
```go
// translateReceipt maps a *events.Receipt to our domain IncomingReceipt.
// Returns false for receipt types we don't persist (Sender, Inactive, Retry).
func translateReceipt(evt *events.Receipt) (IncomingReceipt, bool) {
	var typeStr string
	switch evt.Type {
	case events.ReceiptTypeDelivered:
		typeStr = "delivered"
	case events.ReceiptTypeRead:
		typeStr = "read"
	case events.ReceiptTypePlayed:
		typeStr = "played"
	default:
		return IncomingReceipt{}, false
	}

	ids := make([]string, 0, len(evt.MessageIDs))
	for _, id := range evt.MessageIDs {
		ids = append(ids, string(id))
	}

	return IncomingReceipt{
		MessageIDs: ids,
		ChatJID:    evt.Chat.String(),
		ReaderJID:  evt.Sender.String(),
		Type:       typeStr,
		Timestamp:  evt.Timestamp,
	}, true
}
```

> Note: if whatsmeow's exact field names differ (e.g. `ReceiptTypeDelivered` vs `ReceiptDelivered`), run `go doc go.mau.fi/whatsmeow/types/events.Receipt` and adapt.

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
git commit -m "waclient: onEvent + translateReceipt for events.Receipt"
```

---

## Task 6: service MarkMessageRead + SendTyping + ListReceipts

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
type readFakeWA struct {
	fakeWA
	gotMarkChat string
	gotMarkSender string
	gotMarkMsgID string
	markErr     error
	gotPresenceChat string
	gotPresenceState string
	presenceErr error
}

func (f *readFakeWA) MarkRead(_ context.Context, chatJID, senderJID, messageID string, _ time.Time) error {
	f.gotMarkChat = chatJID
	f.gotMarkSender = senderJID
	f.gotMarkMsgID = messageID
	return f.markErr
}
func (f *readFakeWA) SendChatPresence(_ context.Context, chatJID, state string) error {
	f.gotPresenceChat = chatJID
	f.gotPresenceState = state
	return f.presenceErr
}

func TestMarkMessageReadHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}

	require.NoError(t, s.MarkMessageRead(ctx, "M1"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotMarkChat)
	assert.Equal(t, "alice@s.whatsapp.net", wa.gotMarkSender)
	assert.Equal(t, "M1", wa.gotMarkMsgID)

	got, err := bundle.Chats.Get(ctx, "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 4, got.UnreadCount)
}

func TestMarkMessageReadDecrementClampsAtZero(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 0,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}

	require.NoError(t, s.MarkMessageRead(ctx, "M1"))
	got, err := bundle.Chats.Get(ctx, "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 0, got.UnreadCount, "must not go negative")
}

func TestMarkMessageReadNotFound(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.MarkMessageRead(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMarkMessageReadNotConnected(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{
		fakeWA:  fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		markErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.MarkMessageRead(context.Background(), "M1")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestMarkMessageReadValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.MarkMessageRead(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingComposing(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NoError(t, s.SendTyping(context.Background(), "c@s.whatsapp.net", "composing"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotPresenceChat)
	assert.Equal(t, "composing", wa.gotPresenceState)
}

func TestSendTypingPaused(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NoError(t, s.SendTyping(context.Background(), "c@s.whatsapp.net", "paused"))
	assert.Equal(t, "paused", wa.gotPresenceState)
}

func TestSendTypingValidationBadState(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "c@s.whatsapp.net", "yelling")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingValidationEmptyChatJID(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "", "composing")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{
		fakeWA:      fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		presenceErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "c@s.whatsapp.net", "composing")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestListReceiptsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	require.NoError(t, bundle.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: time.Unix(1000, 0).UTC(),
	}))

	got, err := s.ListReceipts(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "read", got[0].Type)
}

func TestListReceiptsValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListReceipts(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestMarkMessageRead|TestSendTyping|TestListReceipts'
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement on Service**

Edit `internal/service/service.go`. Extend the Service interface:
```go
MarkMessageRead(ctx context.Context, messageID string) error
SendTyping(ctx context.Context, chatJID, state string) error
ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error)
```

Append the methods at the bottom:
```go
func (s *svc) MarkMessageRead(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}

	if err := s.wa.MarkRead(ctx, existing.ChatJID, existing.SenderJID, messageID, time.Now()); err != nil {
		return err
	}

	chat, err := s.bundle.Chats.Get(ctx, existing.ChatJID)
	if err == nil && chat.UnreadCount > 0 {
		chat.UnreadCount--
		if err := s.bundle.Chats.Put(ctx, chat); err != nil {
			s.logger.Warn("decrement unread on mark-read failed", "chat_jid", existing.ChatJID, "err", err)
		}
	}
	return nil
}

func (s *svc) SendTyping(ctx context.Context, chatJID, state string) error {
	if strings.TrimSpace(chatJID) == "" {
		return fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if state != "composing" && state != "paused" {
		return fmt.Errorf("%w: state must be composing or paused", ErrInvalidRequest)
	}
	return s.wa.SendChatPresence(ctx, chatJID, state)
}

func (s *svc) ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Receipts.ListByMessageID(ctx, messageID)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — 11 new tests + existing.

- [ ] **Step 5: Bridge HTTP fakes**

Add 3 stubs to each of the 9 HTTP fake services (status, login_qr, login_phone, logout, messages, chats, contacts, stats, media, reactions test files):

```go
func (f X) MarkMessageRead(context.Context, string) error { return nil }
func (f X) SendTyping(context.Context, string, string) error { return nil }
func (f X) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}
```

Adapt receiver style per fake.

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "service: MarkMessageRead + SendTyping + ListReceipts"
```

---

## Task 7: service handleReceipt + register OnIncomingReceipt

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestHandleReceiptPersistsAll(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &readFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incomingReceipt)

	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1", "M2", "M3"},
		ChatJID:    "c@s.whatsapp.net",
		ReaderJID:  "alice@s.whatsapp.net",
		Type:       "read",
		Timestamp:  time.Unix(1000, 0).UTC(),
	})

	for _, id := range []string{"M1", "M2", "M3"} {
		got, err := bundle.Receipts.ListByMessageID(context.Background(), id)
		require.NoError(t, err)
		require.Len(t, got, 1, "expected receipt for %q", id)
		assert.Equal(t, "read", got[0].Type)
		assert.Equal(t, "alice@s.whatsapp.net", got[0].ReaderJID)
	}
}

func TestHandleReceiptUpsert(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &readFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incomingReceipt)

	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()

	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1"}, ChatJID: "c@s.whatsapp.net",
		ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t1,
	})
	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1"}, ChatJID: "c@s.whatsapp.net",
		ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: t2,
	})

	got, err := bundle.Receipts.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Timestamp.Equal(t2))
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./internal/service/... -run TestHandleReceipt
```

Expected: FAIL — `wa.incomingReceipt` is nil because service.New doesn't register it.

- [ ] **Step 3: Implement handleReceipt + register in New**

Edit `internal/service/service.go`. Find `func New(...)`:
```go
func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &svc{wa: wa, bundle: bundle, mediaStore: mediaStore, logger: logger}
	wa.OnIncomingMessage(s.handleIncoming)
	wa.OnIncomingReceipt(s.handleReceipt) // Plan 07c
	return s
}
```

Append the method:
```go
func (s *svc) handleReceipt(r waclient.IncomingReceipt) {
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
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: handleReceipt persists inbound receipts"
```

---

## Task 8: HTTP handlers + 3 routes

**Files:**
- Create: `internal/transport/http/receipts.go`
- Create: `internal/transport/http/receipts_test.go`
- Create: `internal/transport/http/typing.go`
- Create: `internal/transport/http/typing_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/receipts_test.go`:
```go
package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type fakeReceiptsSvc struct {
	markErr  error
	listResp []store.Receipt
	listErr  error

	gotMarkID string
	gotListID string
}

// minimal Service surface: we only need the methods we touch + enough to satisfy the interface.
func (f *fakeReceiptsSvc) Status(context.Context) (waclient.Status, error)             { return waclient.Status{}, nil }
func (f *fakeReceiptsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)    { return nil, nil }
func (f *fakeReceiptsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f *fakeReceiptsSvc) Logout(context.Context) error                                { return nil }
func (f *fakeReceiptsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) GetChat(context.Context, string) (store.Chat, error) { return store.Chat{}, nil }
func (f *fakeReceiptsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReceiptsSvc) ListContacts(context.Context) ([]store.Contact, error)               { return nil, nil }
func (f *fakeReceiptsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) { return nil, nil }
func (f *fakeReceiptsSvc) Stats(context.Context) (service.Stats, error)                        { return service.Stats{}, nil }
func (f *fakeReceiptsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeReceiptsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReceiptsSvc) DeleteMessage(context.Context, string) error                  { return nil }
func (f *fakeReceiptsSvc) SendReaction(context.Context, string, string) error           { return nil }
func (f *fakeReceiptsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) { return nil, nil }
func (f *fakeReceiptsSvc) MarkMessageRead(_ context.Context, id string) error {
	f.gotMarkID = id
	return f.markErr
}
func (f *fakeReceiptsSvc) SendTyping(context.Context, string, string) error { return nil }
func (f *fakeReceiptsSvc) ListReceipts(_ context.Context, id string) ([]store.Receipt, error) {
	f.gotListID = id
	return f.listResp, f.listErr
}

var _ service.Service = (*fakeReceiptsSvc)(nil)

func TestMarkReadHappyPath(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotMarkID)
}

func TestMarkReadNotFound(t *testing.T) {
	f := &fakeReceiptsSvc{markErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestMarkReadNotConnected(t *testing.T) {
	f := &fakeReceiptsSvc{markErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/read", httpapi.MarkReadHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/read", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestListReceiptsHandlerHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeReceiptsSvc{listResp: []store.Receipt{
		{MessageID: "MID1", ReaderJID: "a@s.whatsapp.net", Type: "read", Timestamp: now},
	}}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/receipts", httpapi.ListReceiptsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/receipts")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Receipts []map[string]any `json:"receipts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Receipts, 1)
	assert.Equal(t, "MID1", body.Receipts[0]["message_id"])
	assert.Equal(t, "read", body.Receipts[0]["type"])
}

func TestListReceiptsHandlerEmpty(t *testing.T) {
	f := &fakeReceiptsSvc{}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/receipts", httpapi.ListReceiptsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/receipts")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Receipts []map[string]any `json:"receipts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Empty(t, body.Receipts)
}
```

Create `internal/transport/http/typing_test.go`:
```go
package http_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

type fakeTypingSvc struct {
	err          error
	gotChat      string
	gotState     string
}

func (f *fakeTypingSvc) Status(context.Context) (waclient.Status, error)                       { return waclient.Status{}, nil }
func (f *fakeTypingSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f *fakeTypingSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f *fakeTypingSvc) Logout(context.Context) error                                          { return nil }
func (f *fakeTypingSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeTypingSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeTypingSvc) GetChat(context.Context, string) (store.Chat, error) { return store.Chat{}, nil }
func (f *fakeTypingSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeTypingSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeTypingSvc) ListContacts(context.Context) ([]store.Contact, error)               { return nil, nil }
func (f *fakeTypingSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) { return nil, nil }
func (f *fakeTypingSvc) Stats(context.Context) (service.Stats, error)                        { return service.Stats{}, nil }
func (f *fakeTypingSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeTypingSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeTypingSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeTypingSvc) DeleteMessage(context.Context, string) error                  { return nil }
func (f *fakeTypingSvc) SendReaction(context.Context, string, string) error           { return nil }
func (f *fakeTypingSvc) ListReactions(context.Context, string) ([]store.Reaction, error) { return nil, nil }
func (f *fakeTypingSvc) MarkMessageRead(context.Context, string) error                { return nil }
func (f *fakeTypingSvc) SendTyping(_ context.Context, chatJID, state string) error {
	f.gotChat = chatJID
	f.gotState = state
	return f.err
}
func (f *fakeTypingSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) {
	return nil, nil
}

var _ service.Service = (*fakeTypingSvc)(nil)

func TestSendTypingHappyPath(t *testing.T) {
	f := &fakeTypingSvc{}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"composing"}`)
	res, err := http.Post(srv.URL+"/v1/chats/c@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "c@s.whatsapp.net", f.gotChat)
	assert.Equal(t, "composing", f.gotState)
}

func TestSendTypingBadJSON(t *testing.T) {
	f := &fakeTypingSvc{}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/chats/c@s.whatsapp.net/typing", "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTypingBadState(t *testing.T) {
	f := &fakeTypingSvc{err: service.ErrInvalidRequest}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"yelling"}`)
	res, err := http.Post(srv.URL+"/v1/chats/c@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendTypingNotConnectedHTTP(t *testing.T) {
	f := &fakeTypingSvc{err: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/chats/{jid}/typing", httpapi.SendTypingHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"state":"composing"}`)
	res, err := http.Post(srv.URL+"/v1/chats/c@s.whatsapp.net/typing", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./internal/transport/http/... -run 'TestMarkRead|TestListReceiptsHandler|TestSendTyping(HappyPath|BadJSON|BadState|NotConnectedHTTP)'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/receipts.go`:
```go
package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

// MarkReadHandler handles POST /v1/messages/{id}/read.
// Body is ignored. 204 success.
func MarkReadHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		err := svc.MarkMessageRead(r.Context(), messageID)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "message.not_found", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}

// ListReceiptsHandler handles GET /v1/messages/{id}/receipts.
// 200 with {"receipts": [...]}.
func ListReceiptsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		receipts, err := svc.ListReceipts(r.Context(), messageID)
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"receipts": encodeReceipts(receipts),
		})
	})
}

func encodeReceipt(r store.Receipt) map[string]any {
	return map[string]any{
		"message_id": r.MessageID,
		"reader_jid": r.ReaderJID,
		"type":       r.Type,
		"ts":         r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func encodeReceipts(rs []store.Receipt) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, encodeReceipt(r))
	}
	return out
}
```

Create `internal/transport/http/typing.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

type sendTypingRequest struct {
	State string `json:"state"`
}

// SendTypingHandler handles POST /v1/chats/{jid}/typing.
// Body: {"state": "composing"|"paused"}. 204 success.
func SendTypingHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatJID := chi.URLParam(r, "jid")
		var req sendTypingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}

		err := svc.SendTyping(r.Context(), chatJID, req.State)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}
```

- [ ] **Step 4: Wire 3 routes**

Edit `internal/transport/http/router.go`. In the auth-protected group, append:
```go
r.Method(http.MethodPost, "/messages/{id}/read", MarkReadHandler(d.Service))
r.Method(http.MethodGet, "/messages/{id}/receipts", ListReceiptsHandler(d.Service))
r.Method(http.MethodPost, "/chats/{jid}/typing", SendTypingHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -race
```

Expected: PASS — 8 new HTTP tests + existing.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/receipts.go internal/transport/http/receipts_test.go internal/transport/http/typing.go internal/transport/http/typing_test.go internal/transport/http/router.go
git commit -m "http: POST /messages/{id}/read + GET /messages/{id}/receipts + POST /chats/{jid}/typing"
```

---

## Task 9: End-to-end smoke test

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

Expected: `app store opened`, `server starting`.

Verify `receipts` table created:
```bash
ls data/whatsmeow-app.db   # confirms file exists; sqlite3 CLI may or may not be installed
```

- [ ] **Step 2: Validation paths**

```bash
# Mark unknown message as read → 404
curl -i -X POST http://127.0.0.1:8080/v1/messages/MID1/read

# Typing with bad JSON → 400
curl -i -X POST -H "Content-Type: application/json" -d 'not json' \
  http://127.0.0.1:8080/v1/chats/c@s.whatsapp.net/typing

# Typing with bad state → 400
curl -i -X POST -H "Content-Type: application/json" -d '{"state":"yelling"}' \
  http://127.0.0.1:8080/v1/chats/c@s.whatsapp.net/typing

# Typing valid (not connected) → 409
curl -i -X POST -H "Content-Type: application/json" -d '{"state":"composing"}' \
  http://127.0.0.1:8080/v1/chats/c@s.whatsapp.net/typing

# GET receipts on no message → 200 with empty array
curl -s http://127.0.0.1:8080/v1/messages/MID1/receipts
```

Expected: 404 / 400 / 400 / 409 / `{"receipts":[]}`.

- [ ] **Step 3: (Optional) Real round-trip with paired account**

If you have a paired account:
- Send a message; recipient reads it.
- `curl http://127.0.0.1:8080/v1/messages/<id>/receipts` shows a `read` row.
- Receive a message; `curl -X POST .../v1/messages/<id>/read` returns 204; `chat.unread_count` decrements.
- `curl -X POST -d '{"state":"composing"}' .../v1/chats/<jid>/typing` → 204; recipient sees "composing…".

- [ ] **Step 4: Stop daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -3 /tmp/wmapi.log
```

Expected: `... msg="server stopped"`.

- [ ] **Step 5: No commit**

---

## Task 10: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update Status section**

Edit `README.md`. Find the Plan 07b entry; append a new line for Plan 07c:
```markdown
- **Plan 07b (reactions)** shipped: ...
- **Plan 07c (read receipts + typing)** shipped: `POST /v1/messages/{id}/read` marks a received message as read (decrements `chats.unread_count`); `POST /v1/chats/{jid}/typing {state}` sends `composing`/`paused` presence; `GET /v1/messages/{id}/receipts` lists who has acked the message. New `receipts` table populated from inbound `events.Receipt`.
```

Update the trailing line:
```markdown
SSE event stream lands in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 07c"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS
- [ ] Daemon boots; `data/whatsmeow-app.db` includes the `receipts` table
- [ ] Manual smoke (Task 9 Step 2): 404 / 400 / 400 / 409 / empty array
- [ ] (Optional with paired account) Task 9 Step 3: full round-trip
- [ ] `git log --oneline` shows ~10 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 08 — group operations** (or whichever milestone comes next from the master design).

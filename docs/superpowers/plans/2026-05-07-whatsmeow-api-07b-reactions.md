# whatsmeow-api Plan 07b — Reactions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bidirectional emoji reactions. `POST /v1/messages/{id}/reactions` (empty emoji clears) + `GET /v1/messages/{id}/reactions`. Inbound reactions auto-persist via a new `reactions` table.

**Architecture:** New `0002_reactions` migration adds the table. New `ReactionStore` interface joins the `Bundle`. `WAClient.SendReaction` builds + sends the reaction proto via whatsmeow's helper; `translateIncoming` detects inbound `*waE2E.ReactionMessage` and surfaces `ReactionTargetID` + `ReactionEmoji` on `IncomingMessage`. Service routes them in `handleIncoming` BEFORE Plan 07a's revoke/edit branches.

**Tech Stack:**
- Go 1.26
- Plan 01–07a stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)
- whatsmeow's `Client.BuildReaction` (or manual `*waE2E.ReactionMessage` proto)

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/store.go` | Modified — `+Reaction` type, `+ReactionStore` interface, `+Bundle.Reactions`. |
| `internal/store/migrations/sqlite/0002_reactions.up.sql` | NEW. |
| `internal/store/migrations/sqlite/0002_reactions.down.sql` | NEW. |
| `internal/store/sqlite/reactions.go` | NEW — `*ReactionStore` implementation. |
| `internal/store/sqlite/reactions_test.go` | NEW. |
| `internal/store/sqlite/store.go` | Modified — wire `*ReactionStore` into `*Store`, expose via `Bundle()`. |
| `internal/waclient/waclient.go` | Modified — `+SendReaction` in interface; `+ReactionTargetID`, `+ReactionEmoji` on `IncomingMessage`. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — `SendReaction` impl; `translateIncoming` reaction branch. |
| `internal/service/service.go` | Modified — `+SendReaction`, `+ListReactions`; `handleIncoming` reaction route BEFORE revoke/edit. |
| `internal/service/service_test.go` | Modified — `+reactionStore` fake; tests. |
| `internal/transport/http/reactions.go` | NEW — `SendReactionHandler`, `ListReactionsHandler`. |
| `internal/transport/http/reactions_test.go` | NEW. |
| `internal/transport/http/router.go` | Modified — +2 routes. |
| Existing HTTP fakes | Modified — bridge stubs for new Service surface. |
| `README.md` | Modified — status section. |

---

## Task 1: Migration + store interface + Bundle field

**Files:**
- Create: `internal/store/migrations/sqlite/0002_reactions.up.sql`
- Create: `internal/store/migrations/sqlite/0002_reactions.down.sql`
- Modify: `internal/store/store.go`

- [ ] **Step 1: Create the up migration**

`internal/store/migrations/sqlite/0002_reactions.up.sql`:
```sql
CREATE TABLE reactions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    sender_jid TEXT NOT NULL,
    emoji      TEXT NOT NULL,
    ts         INTEGER NOT NULL,
    PRIMARY KEY (message_id, sender_jid)
);
CREATE INDEX idx_reactions_message ON reactions(message_id);
```

- [ ] **Step 2: Create the down migration**

`internal/store/migrations/sqlite/0002_reactions.down.sql`:
```sql
DROP INDEX IF EXISTS idx_reactions_message;
DROP TABLE IF EXISTS reactions;
```

- [ ] **Step 3: Extend the store package**

Edit `internal/store/store.go`. Add the type near the existing domain types (`Chat`, `Message`, `Contact`, `MediaRef`, `EventLogEntry`):
```go
// Reaction is an emoji reaction on a message. PK is (MessageID, SenderJID) —
// each user has at most one current reaction per message.
type Reaction struct {
	MessageID string
	SenderJID string
	Emoji     string
	Timestamp time.Time
}
```

Add the interface near the others (`ChatStore`, `MessageStore`, etc.):
```go
// ReactionStore manages the reactions table.
type ReactionStore interface {
	Put(ctx context.Context, r Reaction) error
	Delete(ctx context.Context, messageID, senderJID string) error
	ListByMessageID(ctx context.Context, messageID string) ([]Reaction, error)
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
	Reactions ReactionStore // Plan 07b
}
```

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: build fails — `internal/store/sqlite.Store` doesn't yet construct a `*ReactionStore`, and the in-memory bundle fakes in `internal/service/service_test.go` don't yet have a `reactionStore` field. Tasks 2 and 3 fix this.

But wait — the `Bundle` type is just a struct of interfaces. Adding a new field doesn't break consumers; nil interfaces are fine until someone calls a method on them. So `go build` should still pass for everything except where the migration files are referenced (which is only by `mediastore`/`sqlitestore` — they don't reference reactions yet).

Verify:
```bash
go build ./...
go vet ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/migrations/sqlite/0002_reactions.up.sql internal/store/migrations/sqlite/0002_reactions.down.sql
git commit -m "store: add 0002_reactions migration + ReactionStore interface"
```

---

## Task 2: SQLite ReactionStore impl

**Files:**
- Create: `internal/store/sqlite/reactions.go`
- Create: `internal/store/sqlite/reactions_test.go`
- Modify: `internal/store/sqlite/store.go` (wire ReactionStore into *Store + Bundle)

- [ ] **Step 1: Write the failing tests**

Create `internal/store/sqlite/reactions_test.go`:
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

// seedMessageForReactions seeds a chat + message so the reactions FK is satisfied.
func seedMessageForReactions(t *testing.T, b store.Bundle, chatJID, messageID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, b.Chats.Put(ctx, store.Chat{JID: chatJID, Kind: "user"}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: messageID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}))
}

func TestReactionPutGetList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	b := s.Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")

	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: now,
	}))
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "bob@s.whatsapp.net", Emoji: "❤️", Timestamp: now,
	}))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Sorted by sender_jid ASC.
	assert.Equal(t, "alice@s.whatsapp.net", got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
	assert.Equal(t, "bob@s.whatsapp.net", got[1].SenderJID)
	assert.Equal(t, "❤️", got[1].Emoji)
	assert.True(t, got[0].Timestamp.Equal(now))
}

func TestReactionPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")

	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: t1,
	}))
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "❤️", Timestamp: t2,
	}))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "❤️", got[0].Emoji)
	assert.True(t, got[0].Timestamp.Equal(t2))
}

func TestReactionDelete(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	require.NoError(t, b.Reactions.Delete(ctx, "M1", "alice@s.whatsapp.net"))

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Idempotent — deleting a non-existent reaction is a no-op.
	require.NoError(t, b.Reactions.Delete(ctx, "M1", "nobody@s.whatsapp.net"))
}

func TestReactionListEmpty(t *testing.T) {
	got, err := newTestStore(t).Bundle().Reactions.ListByMessageID(context.Background(), "no-such-message")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReactionFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessageForReactions(t, b, "c@s.whatsapp.net", "M1")
	require.NoError(t, b.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	// Hard-delete the parent message via a sibling sql.DB (FK cascade requires foreign_keys=on).
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, "M1")
	require.NoError(t, err)

	got, err := b.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got, "reactions must cascade away when parent message is deleted")
}
```

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/store/sqlite/... -run TestReaction
```

Expected: FAIL — `Bundle.Reactions` is nil (Task 1 added the field but Task 2 has not yet wired the impl).

- [ ] **Step 3: Implement ReactionStore**

Create `internal/store/sqlite/reactions.go`:
```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

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

- [ ] **Step 4: Wire into sqlite.Store**

Edit `internal/store/sqlite/store.go`. Find the `Store` struct and add a `reactions` field:
```go
type Store struct {
	db *sql.DB

	chats     *ChatStore
	messages  *MessageStore
	contacts  *ContactStore
	media     *MediaStore
	events    *EventsLog
	kv        *KVStore
	reactions *ReactionStore  // Plan 07b
}
```

In the `New` function, after constructing the existing sub-stores, add:
```go
s.reactions = &ReactionStore{db: db}
```

In `Bundle()`, add the field:
```go
return store.Bundle{
	Chats:     s.chats,
	Messages:  s.messages,
	Contacts:  s.contacts,
	Media:     s.media,
	Events:    s.events,
	KV:        s.kv,
	Reactions: s.reactions, // Plan 07b
}
```

- [ ] **Step 5: Add a verification to the existing TestNewCreatesAllTables**

Edit `internal/store/sqlite/store_test.go`. Find `expectedTables`:
```go
expectedTables := []string{
	"chats", "contacts", "events_log", "kv", "media", "messages", "messages_fts",
}
```

Replace with:
```go
expectedTables := []string{
	"chats", "contacts", "events_log", "kv", "media", "messages", "messages_fts", "reactions",
}
```

Also add to `TestBundleFieldsNonNil`:
```go
b := s.Bundle()
assert.NotNil(t, b.Chats)
assert.NotNil(t, b.Messages)
assert.NotNil(t, b.Contacts)
assert.NotNil(t, b.Media)
assert.NotNil(t, b.Events)
assert.NotNil(t, b.KV)
assert.NotNil(t, b.Reactions)  // Plan 07b
```

- [ ] **Step 6: Run all sqlite tests**

```bash
go test ./internal/store/sqlite/... -v
```

Expected: all PASS — existing 28 tests + 5 new reactions tests + the schema-table check.

- [ ] **Step 7: Service tests bridge — extend the in-memory bundle**

Edit `internal/service/service_test.go`. Find the `newInMemoryBundle()` helper and the existing `mediaStore` / `eventsStore` / `kvStore` fakes. Add:

After `kvStore`:
```go
type reactionStore struct {
	mu sync.Mutex
	m  map[string]store.Reaction // key: messageID + "|" + senderJID
}

func (s *reactionStore) Put(_ context.Context, r store.Reaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]store.Reaction{}
	}
	s.m[r.MessageID+"|"+r.SenderJID] = r
	return nil
}

func (s *reactionStore) Delete(_ context.Context, messageID, senderJID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, messageID+"|"+senderJID)
	return nil
}

func (s *reactionStore) ListByMessageID(_ context.Context, messageID string) ([]store.Reaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Reaction, 0)
	for _, r := range s.m {
		if r.MessageID == messageID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SenderJID < out[j].SenderJID })
	return out, nil
}
```

`sync` is already imported (Plan 06 added it). `sort` is already imported (Plan 05 added it).

Update `newInMemoryBundle` to construct and return the new fake. Find:
```go
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
```

Replace with:
```go
func newInMemoryBundle() (store.Bundle, *memChats, *memMessages, *memContacts, *reactionStore) {
	c := memChats{}
	m := memMessages{}
	co := memContacts{}
	rs := &reactionStore{m: map[string]store.Reaction{}}
	return store.Bundle{
		Chats:     &chatStore{m: c},
		Messages:  &messageStore{m: m},
		Contacts:  &contactStore{m: co},
		Media:     &mediaStore{},
		Events:    &eventsStore{},
		KV:        &kvStore{m: map[string]string{}},
		Reactions: rs,
	}, &c, &m, &co, rs
}
```

The signature now returns 5 values. EVERY existing call site must be updated. Use `grep -n "newInMemoryBundle()" internal/service/service_test.go` to find them. Each call like:
```go
bundle, chats, msgs, contacts := newInMemoryBundle()
```
becomes:
```go
bundle, chats, msgs, contacts, _ := newInMemoryBundle()
```
(with `_` for the reactionStore unless the test needs it). There are roughly 10-15 call sites — replace each.

- [ ] **Step 8: Run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/sqlite/reactions.go internal/store/sqlite/reactions_test.go internal/store/sqlite/store.go internal/store/sqlite/store_test.go internal/service/service_test.go
git commit -m "store/sqlite: ReactionStore impl with FK cascade test; bundle wired"
```

---

## Task 3: waclient interface + adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service_test.go` (fakeWA stub)

- [ ] **Step 1: Extend the interface and IncomingMessage**

Edit `internal/waclient/waclient.go`. Find `IncomingMessage`:
```go
type IncomingMessage struct {
	// existing fields (Plan 04 + 06 + 07a)
	ReactionTargetID string  // if set, this event is a reaction targeting that message
	ReactionEmoji    string  // empty string means "clear my reaction"
}
```

Find `WAClient` interface and append:
```go
// Plan 07b
SendReaction(ctx context.Context, chatJID, originalMessageID, emoji string) error
```

- [ ] **Step 2: Add adapter stub**

Edit `internal/waclient/whatsmeow_adapter.go`. Insert before the `var _ WAClient = (*Adapter)(nil)` line:
```go
// SendReaction is implemented in Plan 07b Task 4.
func (a *Adapter) SendReaction(ctx context.Context, chatJID, originalMessageID, emoji string) error {
	_ = ctx; _ = chatJID; _ = originalMessageID; _ = emoji
	return errors.New("waclient: SendReaction not yet implemented")
}
```

Add `"errors"` to the import block if not already present.

- [ ] **Step 3: Bridge fakeWA**

Edit `internal/service/service_test.go`. Find `fakeWA` methods. Add:
```go
func (f *fakeWA) SendReaction(context.Context, string, string, string) error {
	return nil
}
```

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
git commit -m "waclient: SendReaction interface + IncomingMessage reaction fields (stub)"
```

---

## Task 4: waclient SendReaction adapter implementation

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test (real WhatsApp). Manual smoke (Task 9) covers it.

- [ ] **Step 1: Inspect whatsmeow API**

```bash
go doc go.mau.fi/whatsmeow.Client.BuildReaction
go doc go.mau.fi/whatsmeow/proto/waE2E.ReactionMessage
```

Confirm the helper's signature. Likely:
```go
func (cli *Client) BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message
```

Or similar. If absent, build the proto manually (see step 2's note).

- [ ] **Step 2: Replace the SendReaction stub**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the stub from Task 3 and replace with:
```go
// SendReaction sends an emoji reaction to originalMessageID in chatJID.
// Empty emoji string clears the daemon's reaction.
func (a *Adapter) SendReaction(ctx context.Context, chatJID, originalMessageID, emoji string) error {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return ErrNotConnected
	}
	senderJID := *a.client.Store.ID
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat_jid: %w", err)
	}

	msg := client.BuildReaction(to, senderJID, originalMessageID, emoji)

	if _, err := client.SendMessage(ctx, to, msg); err != nil {
		return fmt.Errorf("send reaction: %w", err)
	}
	return nil
}
```

> **Note:** if `Client.BuildReaction` doesn't exist, build manually:
> ```go
> reactionType := false // FromMe = false unless we're reacting to our own message
> msg := &waE2E.Message{
>     ReactionMessage: &waE2E.ReactionMessage{
>         Key: &waCommon.MessageKey{
>             FromMe:    proto.Bool(reactionType),
>             ID:        proto.String(originalMessageID),
>             RemoteJID: proto.String(chatJID),
>         },
>         Text:              proto.String(emoji),
>         SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
>     },
> }
> ```
> Add `"go.mau.fi/whatsmeow/proto/waCommon"` import in that case.

Remove the `"errors"` import if it's no longer used (the stub used `errors.New`; the real impl uses `fmt.Errorf` and `ErrNotConnected` from waclient package).

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
git commit -m "waclient: implement SendReaction via Client.BuildReaction"
```

---

## Task 5: waclient translateIncoming reaction branch

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

- [ ] **Step 1: Update translateIncoming**

Edit `internal/waclient/whatsmeow_adapter.go`. Find `translateIncoming`. Currently it routes ProtocolMessage and falls through to `messageKindAndBody`. Add a reaction branch BEFORE the protocol-message check:

Current shape:
```go
func translateIncoming(a *Adapter, evt *events.Message) (IncomingMessage, bool) {
	if evt.Info.IsFromMe {
		return IncomingMessage{}, false
	}
	if evt.Message != nil && evt.Message.ProtocolMessage != nil {
		return translateProtocol(evt)
	}
	kind, body, downloader, ok := messageKindAndBody(a, evt.Message)
	// ...
}
```

Change to:
```go
func translateIncoming(a *Adapter, evt *events.Message) (IncomingMessage, bool) {
	if evt.Info.IsFromMe {
		return IncomingMessage{}, false
	}
	if evt.Message != nil && evt.Message.ReactionMessage != nil {
		return translateReaction(evt)
	}
	if evt.Message != nil && evt.Message.ProtocolMessage != nil {
		return translateProtocol(evt)
	}
	kind, body, downloader, ok := messageKindAndBody(a, evt.Message)
	// ... (unchanged)
}
```

Append the new helper after `translateProtocol`:
```go
// translateReaction handles inbound *waE2E.ReactionMessage events.
func translateReaction(evt *events.Message) (IncomingMessage, bool) {
	rm := evt.Message.ReactionMessage
	return IncomingMessage{
		ID:               evt.Info.ID,
		ChatJID:          evt.Info.Chat.String(),
		ChatKind:         ChatKindFromJID(evt.Info.Chat.String()),
		SenderJID:        evt.Info.Sender.String(),
		Timestamp:        evt.Info.Timestamp,
		ReactionTargetID: rm.GetKey().GetID(),
		ReactionEmoji:    rm.GetText(),
	}, true
}
```

- [ ] **Step 2: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. (Service tests don't exercise reaction inbound yet — Task 7 adds those.)

- [ ] **Step 3: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: translateIncoming detects ReactionMessage"
```

---

## Task 6: Service SendReaction + ListReactions

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
type reactionFakeWA struct {
	fakeWA
	gotReactionChatJID string
	gotReactionMsgID   string
	gotReactionEmoji   string
	reactionErr        error
}

func (f *reactionFakeWA) SendReaction(_ context.Context, chatJID, originalMessageID, emoji string) error {
	f.gotReactionChatJID = chatJID
	f.gotReactionMsgID = originalMessageID
	f.gotReactionEmoji = emoji
	return f.reactionErr
}

func TestSendReactionHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, rs := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	require.NoError(t, s.SendReaction(ctx, "M1", "👍"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotReactionChatJID)
	assert.Equal(t, "M1", wa.gotReactionMsgID)
	assert.Equal(t, "👍", wa.gotReactionEmoji)

	// Local reactions store has our reaction.
	got, err := bundle.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, jid, got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
	_ = rs
}

func TestSendReactionClear(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	// Pre-seed: we already had a reaction.
	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: jid, Emoji: "👍", Timestamp: time.Now(),
	}))

	require.NoError(t, s.SendReaction(ctx, "M1", ""))
	assert.Equal(t, "", wa.gotReactionEmoji)

	// Local reaction is gone.
	got, err := bundle.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSendReactionNotFound(t *testing.T) {
	bundle, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendReaction(context.Background(), "missing", "👍")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestSendReactionNotConnected(t *testing.T) {
	bundle, _, msgs, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{
		fakeWA:      fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		reactionErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.SendReaction(context.Background(), "M1", "👍")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))

	// Local store NOT touched.
	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSendReactionValidation(t *testing.T) {
	bundle, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendReaction(context.Background(), "", "👍")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListReactionsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Unix(1000, 0).UTC(),
	}))

	got, err := s.ListReactions(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "👍", got[0].Emoji)
}

func TestListReactionsValidation(t *testing.T) {
	bundle, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListReactions(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestSendReaction|TestListReactions'
```

Expected: FAIL — `(*svc).SendReaction` and `(*svc).ListReactions` undefined.

- [ ] **Step 3: Implement on Service**

Edit `internal/service/service.go`. Extend the Service interface:
```go
SendReaction(ctx context.Context, messageID, emoji string) error
ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error)
```

Append the methods at the bottom of the file:
```go
func (s *svc) SendReaction(ctx context.Context, messageID, emoji string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}

	if err := s.wa.SendReaction(ctx, existing.ChatJID, messageID, emoji); err != nil {
		return err
	}

	ourJID := ""
	if st := s.wa.Status(); st.JID != nil {
		ourJID = *st.JID
	}

	if emoji == "" {
		if err := s.bundle.Reactions.Delete(ctx, messageID, ourJID); err != nil {
			s.logger.Warn("clear local reaction failed", "id", messageID, "err", err)
		}
		return nil
	}

	if err := s.bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: messageID,
		SenderJID: ourJID,
		Emoji:     emoji,
		Timestamp: time.Now(),
	}); err != nil {
		s.logger.Warn("persist local reaction failed", "id", messageID, "err", err)
	}
	return nil
}

func (s *svc) ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Reactions.ListByMessageID(ctx, messageID)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — 7 new reaction tests + existing.

- [ ] **Step 5: Bridge HTTP fakes**

The 9 HTTP test fakes have `var _ service.Service = ...` checks. Add stubs to each (status_test, login_qr_test, login_phone_test, logout_test, messages_test, chats_test, contacts_test, stats_test, media_test, reactions_test (last one created in Task 8)):

For each fake X:
```go
func (f X) SendReaction(context.Context, string, string) error { return nil }
func (f X) ListReactions(context.Context, string) ([]store.Reaction, error) {
	return nil, nil
}
```

(Adapt `f X` and value/pointer receiver to each fake.)

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "service: SendReaction + ListReactions"
```

---

## Task 7: Service handleIncoming reaction routing

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestHandleIncomingReactionPut(t *testing.T) {
	bundle, _, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	wa.incoming(waclient.IncomingMessage{
		ID:               "EVT1",
		ChatJID:          "c@s.whatsapp.net",
		ChatKind:         "user",
		SenderJID:        "alice@s.whatsapp.net",
		Timestamp:        time.Unix(1000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "👍",
	})

	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alice@s.whatsapp.net", got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
}

func TestHandleIncomingReactionClear(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT2", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "alice@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "",
	})

	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHandleIncomingReactionDoesNotBumpUnread(t *testing.T) {
	bundle, chats, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "alice@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "👍",
	})

	chat, err := bundle.Chats.Get(context.Background(), "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 5, chat.UnreadCount, "reaction must not bump unread_count")
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./internal/service/... -run TestHandleIncomingReaction
```

Expected: FAIL — handleIncoming doesn't route reactions yet.

- [ ] **Step 3: Update handleIncoming**

Edit `internal/service/service.go`. Find `func (s *svc) handleIncoming(...)`. Insert at the very top (BEFORE Plan 07a's revoke/edit branches):
```go
func (s *svc) handleIncoming(msg waclient.IncomingMessage) {
	ctx := context.Background()

	// Plan 07b: route reactions BEFORE revoke/edit/normal paths.
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

	// Plan 07a: revoke + edit branches (existing) ...
	// Plan 04 + 06: existing path for normal messages + media ...
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — 3 new tests + existing.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: handleIncoming routes reactions before edit/revoke/normal"
```

---

## Task 8: HTTP reactions handlers

**Files:**
- Create: `internal/transport/http/reactions.go`
- Create: `internal/transport/http/reactions_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/reactions_test.go`:
```go
package http_test

import (
	"bytes"
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

type fakeReactionsSvc struct {
	sendErr  error
	listResp []store.Reaction
	listErr  error

	gotMessageID string
	gotEmoji     string
	gotListID    string
}

func (f *fakeReactionsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeReactionsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) Logout(context.Context) error { return nil }
func (f *fakeReactionsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeReactionsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeReactionsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeReactionsSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeReactionsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeReactionsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeReactionsSvc) DeleteMessage(context.Context, string) error { return nil }
func (f *fakeReactionsSvc) SendReaction(_ context.Context, messageID, emoji string) error {
	f.gotMessageID = messageID
	f.gotEmoji = emoji
	return f.sendErr
}
func (f *fakeReactionsSvc) ListReactions(_ context.Context, messageID string) ([]store.Reaction, error) {
	f.gotListID = messageID
	return f.listResp, f.listErr
}

var _ service.Service = (*fakeReactionsSvc)(nil)

func TestSendReactionHappyPath(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "MID1", f.gotMessageID)
	assert.Equal(t, "👍", f.gotEmoji)
}

func TestSendReactionEmptyClears(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":""}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "", f.gotEmoji)
}

func TestSendReactionBadJSON(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendReactionNotFound(t *testing.T) {
	f := &fakeReactionsSvc{sendErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestSendReactionNotConnected(t *testing.T) {
	f := &fakeReactionsSvc{sendErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Post("/v1/messages/{id}/reactions", httpapi.SendReactionHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"emoji":"👍"}`)
	res, err := http.Post(srv.URL+"/v1/messages/MID1/reactions", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestListReactionsHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeReactionsSvc{listResp: []store.Reaction{
		{MessageID: "MID1", SenderJID: "a@s.whatsapp.net", Emoji: "👍", Timestamp: now},
	}}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/reactions", httpapi.ListReactionsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/reactions")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Reactions []map[string]any `json:"reactions"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Reactions, 1)
	assert.Equal(t, "MID1", body.Reactions[0]["message_id"])
	assert.Equal(t, "a@s.whatsapp.net", body.Reactions[0]["sender_jid"])
	assert.Equal(t, "👍", body.Reactions[0]["emoji"])
}

func TestListReactionsEmpty(t *testing.T) {
	f := &fakeReactionsSvc{}
	r := chi.NewRouter()
	r.Get("/v1/messages/{id}/reactions", httpapi.ListReactionsHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/messages/MID1/reactions")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Reactions []map[string]any `json:"reactions"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Empty(t, body.Reactions)
}
```

- [ ] **Step 2: Confirm fail**

```bash
go test ./internal/transport/http/... -run 'TestSendReaction|TestListReactions'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/reactions.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

type sendReactionRequest struct {
	Emoji string `json:"emoji"`
}

// SendReactionHandler handles POST /v1/messages/{id}/reactions.
// Body: {"emoji": "..."}. Empty emoji clears the daemon's reaction.
func SendReactionHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		var req sendReactionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}

		err := svc.SendReaction(r.Context(), messageID, req.Emoji)
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

// ListReactionsHandler handles GET /v1/messages/{id}/reactions.
// 200 with {"reactions": [{message_id, sender_jid, emoji, ts}, ...]}.
func ListReactionsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		reactions, err := svc.ListReactions(r.Context(), messageID)
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
			"reactions": encodeReactions(reactions),
		})
	})
}

func encodeReaction(r store.Reaction) map[string]any {
	return map[string]any{
		"message_id": r.MessageID,
		"sender_jid": r.SenderJID,
		"emoji":      r.Emoji,
		"ts":         r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func encodeReactions(rs []store.Reaction) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, encodeReaction(r))
	}
	return out
}
```

- [ ] **Step 4: Wire the routes**

Edit `internal/transport/http/router.go`. In the auth-protected group, append:
```go
r.Method(http.MethodPost, "/messages/{id}/reactions", SendReactionHandler(d.Service))
r.Method(http.MethodGet, "/messages/{id}/reactions", ListReactionsHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -race
```

Expected: PASS — 7 new HTTP tests + existing.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/reactions.go internal/transport/http/reactions_test.go internal/transport/http/router.go
git commit -m "http: POST + GET /v1/messages/{id}/reactions"
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

Expected: `app store opened`, `server starting`, no errors.

Verify migration ran:
```bash
sqlite3 data/whatsmeow-app.db '.tables' | grep reactions
```

Expected: `reactions` listed.

- [ ] **Step 2: Validation paths**

```bash
# Bad JSON
curl -i -X POST -H "Content-Type: application/json" -d 'not json' \
  http://127.0.0.1:8080/v1/messages/MID1/reactions
# → 400 request.invalid

# Unknown message id (daemon not connected → either 404 or 409 depending on order; spec says lookup happens first → 404)
curl -i -X POST -H "Content-Type: application/json" -d '{"emoji":"👍"}' \
  http://127.0.0.1:8080/v1/messages/MID1/reactions
# → 404 message.not_found

# GET on no message
curl -s http://127.0.0.1:8080/v1/messages/MID1/reactions
# → {"reactions":[]}
```

- [ ] **Step 3: (Optional) Real round-trip with paired account**

If you've paired:
```bash
JID="<YOUR_JID>"
# Send a message
ID=$(curl -s -X POST -H "Content-Type: application/json" \
  -d "{\"chat_jid\":\"$JID\",\"text\":\"react to me\"}" \
  http://127.0.0.1:8080/v1/messages | jq -r .id)

# React
curl -X POST -H "Content-Type: application/json" -d '{"emoji":"👍"}' \
  http://127.0.0.1:8080/v1/messages/$ID/reactions
# → 204

# Verify locally
curl -s http://127.0.0.1:8080/v1/messages/$ID/reactions
# → {"reactions":[{"message_id":"...","sender_jid":"<your_jid>","emoji":"👍","ts":"..."}]}

sqlite3 data/whatsmeow-app.db 'SELECT * FROM reactions'
# → row exists

# Clear
curl -X POST -H "Content-Type: application/json" -d '{"emoji":""}' \
  http://127.0.0.1:8080/v1/messages/$ID/reactions
# → 204; reactions list empty again

# From another phone, react to a message we sent. Wait ~3s.
curl -s http://127.0.0.1:8080/v1/messages/$ID/reactions
# → reaction from the other phone appears
```

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

Edit `README.md`. Find the Plan 07a entry and append a new line for Plan 07b:
```markdown
- **Plan 07a (replies + edits + deletes)** shipped: `POST /v1/messages` accepts `reply_to`; `PATCH /v1/messages/{id}` edits an outbound message (owner-only, 403 otherwise); `DELETE /v1/messages/{id}` revokes via whatsmeow's REVOKE ProtocolMessage. Inbound REVOKE / MESSAGE_EDIT events from whatsmeow update local rows (`deleted_at`, `body` + `edited_at`).
- **Plan 07b (reactions)** shipped: `POST /v1/messages/{id}/reactions {emoji}` adds or clears (empty emoji) a reaction; `GET /v1/messages/{id}/reactions` lists all reactions for a message. New `reactions` table (FK-cascade with messages). Inbound reaction events auto-persist.
```

Update the trailing line:
```markdown
Read receipts + typing land in Plan 07c; SSE event stream in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 07b"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS
- [ ] Daemon boots; `data/whatsmeow-app.db` includes the `reactions` table
- [ ] Manual smoke (Task 9 Steps 1-2): validation 400s; 404 for unknown id; GET returns empty array
- [ ] (Optional with paired account) Task 9 Step 3: react/clear round-trip; inbound reaction reflected locally
- [ ] `git log --oneline` shows ~10 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 07c — read receipts + typing**.

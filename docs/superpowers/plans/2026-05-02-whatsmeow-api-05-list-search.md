# whatsmeow-api Plan 05 — List + Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship seven read-side HTTP endpoints over Plan 03's app store: chat list/detail/messages, message search, contact list/search, and stats. All cursor-paginated where applicable; contacts return all rows.

**Architecture:** Extend the store interfaces (ChatStore.List signature change for cursor pagination, plus Count / TotalUnread / ContactStore.Search). Add seven Service methods + a `Stats` value type. Four HTTP handler files (`chats.go` new, extend `messages.go`, `contacts.go` new, `stats.go` new), seven new routes in the existing auth-protected group. No new dependencies, no schema migrations.

**Tech Stack:**
- Go 1.26
- Plan 01–04 stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/store.go` | Modified — extend ChatStore (List sig, Count, TotalUnread), MessageStore (Count), ContactStore (Search, Count). |
| `internal/store/sqlite/chats.go` | Modified — new List signature, Count, TotalUnread. |
| `internal/store/sqlite/chats_test.go` | Modified — rewrite TestChatList; add TestChatCount, TestChatTotalUnread. |
| `internal/store/sqlite/messages.go` | Modified — Count. |
| `internal/store/sqlite/messages_test.go` | Modified — TestMessageCount. |
| `internal/store/sqlite/contacts.go` | Modified — Search, Count. |
| `internal/store/sqlite/contacts_test.go` | Modified — TestContactSearch, TestContactCount. |
| `internal/service/service.go` | Modified — Stats type, 7 new methods (clamp + delegate). |
| `internal/service/service_test.go` | Modified — extend in-memory bundle, +tests for the 7 methods. |
| `internal/transport/http/chats.go` | NEW — ListChatsHandler, GetChatHandler, ListMessagesByChatHandler. |
| `internal/transport/http/chats_test.go` | NEW. |
| `internal/transport/http/messages.go` | Modified — +SearchMessagesHandler. |
| `internal/transport/http/messages_test.go` | Modified — +TestSearchMessages*. |
| `internal/transport/http/contacts.go` | NEW — ListContactsHandler, SearchContactsHandler. |
| `internal/transport/http/contacts_test.go` | NEW. |
| `internal/transport/http/stats.go` | NEW — StatsHandler. |
| `internal/transport/http/stats_test.go` | NEW. |
| `internal/transport/http/router.go` | Modified — +7 routes. |
| `README.md` | Modified — status section. |

---

## Task 1: ChatStore.List signature change + cursor pagination

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/sqlite/chats.go`
- Modify: `internal/store/sqlite/chats_test.go`
- Modify: `internal/service/service_test.go` (fake `chatStore.List` signature only)

The most invasive change in Plan 05. Two existing call sites change: the SQLite test, and the in-memory bundle fake.

- [ ] **Step 1: Update the interface in store.go**

Edit `internal/store/store.go`. Find the `ChatStore` interface:
```go
type ChatStore interface {
	Put(ctx context.Context, c Chat) error
	Get(ctx context.Context, jid string) (Chat, error)
	List(ctx context.Context, includeArchived bool) ([]Chat, error)
	SetArchived(ctx context.Context, jid string, archived bool) error
}
```

Replace with:
```go
type ChatStore interface {
	Put(ctx context.Context, c Chat) error
	Get(ctx context.Context, jid string) (Chat, error)
	List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]Chat, error)
	SetArchived(ctx context.Context, jid string, archived bool) error
}
```

- [ ] **Step 2: Update the SQLite implementation**

Edit `internal/store/sqlite/chats.go`. Find the existing `List`:
```go
func (s *ChatStore) List(ctx context.Context, includeArchived bool) ([]store.Chat, error) {
	q := `SELECT ` + chatColumns + ` FROM chats`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	q += ` ORDER BY last_msg_at DESC NULLS LAST, jid ASC`
	...
}
```

Replace with:
```go
func (s *ChatStore) List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	q := `SELECT ` + chatColumns + ` FROM chats`
	var conds []string
	var args []any
	if !includeArchived {
		conds = append(conds, `archived = 0`)
	}
	if !beforeMsgAt.IsZero() {
		conds = append(conds, `(last_msg_at IS NOT NULL AND last_msg_at < ?)`)
		args = append(args, beforeMsgAt.Unix())
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY last_msg_at DESC NULLS LAST, jid ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("chats list: %w", err)
	}
	defer rows.Close()
	var out []store.Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, fmt.Errorf("chats list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

Add `"strings"` to the import block of `chats.go` if not already present.

- [ ] **Step 3: Rewrite TestChatList**

Edit `internal/store/sqlite/chats_test.go`. Replace the existing `TestChatList`:
```go
func TestChatList(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats

	t1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: t1}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Name: "B", Kind: "user", LastMsgAt: t3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Name: "C", Kind: "user", LastMsgAt: t2, Archived: true}))

	// includeArchived=false, no cursor, big limit → 2 non-archived ordered by last_msg_at DESC
	got, err := chats.List(ctx, time.Time{}, 100, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[1].JID)

	// includeArchived=true → all 3
	got, err = chats.List(ctx, time.Time{}, 100, true)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// cursor: before t3 → only a (archived c excluded)
	got, err = chats.List(ctx, t3, 100, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)

	// limit=1 from newest → b only
	got, err = chats.List(ctx, time.Time{}, 1, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
}
```

- [ ] **Step 4: Update the in-memory fake's List signature**

Edit `internal/service/service_test.go`. Find the fake `chatStore.List`:
```go
func (s *chatStore) List(context.Context, bool) ([]store.Chat, error) { return nil, nil }
```

Replace with a smarter version that returns seeded chats filtered by archive flag and limit (cursor is honored too — it's a simple in-memory filter):
```go
func (s *chatStore) List(_ context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	var out []store.Chat
	for _, c := range s.m {
		if !includeArchived && c.Archived {
			continue
		}
		if !before.IsZero() && (c.LastMsgAt.IsZero() || !c.LastMsgAt.Before(before)) {
			continue
		}
		out = append(out, c)
	}
	// sort by last_msg_at DESC, jid ASC for stability
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastMsgAt.Equal(out[j].LastMsgAt) {
			return out[i].LastMsgAt.After(out[j].LastMsgAt)
		}
		return out[i].JID < out[j].JID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

Add `"sort"` to the imports of `service_test.go` if not already present.

- [ ] **Step 5: Build, vet, run all tests**

```bash
cd /home/askar/src/whatsmeow-api
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. The new TestChatList covers the new pagination contract; existing service tests still call `List` only via Plan 04 paths (none directly), so they're unaffected.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/sqlite/chats.go internal/store/sqlite/chats_test.go internal/service/service_test.go
git commit -m "store: ChatStore.List takes cursor + limit + archived flag"
```

---

## Task 2: Count methods + ContactStore.Search

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/sqlite/chats.go`
- Modify: `internal/store/sqlite/chats_test.go`
- Modify: `internal/store/sqlite/messages.go`
- Modify: `internal/store/sqlite/messages_test.go`
- Modify: `internal/store/sqlite/contacts.go`
- Modify: `internal/store/sqlite/contacts_test.go`

Adds five new methods across three stores. Pure additions (no signature changes).

- [ ] **Step 1: Extend the interfaces in store.go**

Edit `internal/store/store.go`. Add to the existing interface declarations:

In `ChatStore`:
```go
	Count(ctx context.Context) (int, error)
	TotalUnread(ctx context.Context) (int, error)
```

In `MessageStore`:
```go
	Count(ctx context.Context) (int, error)
```

In `ContactStore`:
```go
	Search(ctx context.Context, query string, limit int) ([]Contact, error)
	Count(ctx context.Context) (int, error)
```

- [ ] **Step 2: Add the failing tests**

Append to `internal/store/sqlite/chats_test.go`:
```go
func TestChatCount(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats

	n, err := chats.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Kind: "user"}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Kind: "user"}))
	n, err = chats.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestChatTotalUnread(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats

	got, err := chats.TotalUnread(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, got)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Kind: "user", UnreadCount: 3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Kind: "user", UnreadCount: 0}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 7}))

	got, err = chats.TotalUnread(ctx)
	require.NoError(t, err)
	assert.Equal(t, 10, got)
}
```

Append to `internal/store/sqlite/messages_test.go`:
```go
func TestMessageCount(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	n, err := b.Messages.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: "M1", ChatJID: chat, SenderJID: chat, Timestamp: time.Unix(100, 0).UTC(),
		Kind: "text", Body: "a",
	}))
	require.NoError(t, b.Messages.Put(ctx, store.Message{
		ID: "M2", ChatJID: chat, SenderJID: chat, Timestamp: time.Unix(200, 0).UTC(),
		Kind: "text", Body: "b",
	}))
	n, err = b.Messages.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Soft-deleted messages are excluded.
	require.NoError(t, b.Messages.SoftDelete(ctx, "M1", time.Unix(300, 0).UTC()))
	n, err = b.Messages.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}
```

Append to `internal/store/sqlite/contacts_test.go`:
```go
func TestContactCount(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts

	n, err := cs.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	require.NoError(t, cs.Put(ctx, store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "b@s.whatsapp.net", PushName: "B"}))
	n, err = cs.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestContactSearch(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts

	require.NoError(t, cs.Put(ctx, store.Contact{JID: "1@s.whatsapp.net", PushName: "Alice", FullName: "Alice Anderson"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "2@s.whatsapp.net", PushName: "Bob", BusinessName: "ACME Inc"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "3@s.whatsapp.net", PushName: "carol"}))

	// push_name match (case-insensitive)
	got, err := cs.Search(ctx, "ALICE", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "1@s.whatsapp.net", got[0].JID)

	// full_name match
	got, err = cs.Search(ctx, "anderson", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "1@s.whatsapp.net", got[0].JID)

	// business_name match
	got, err = cs.Search(ctx, "acme", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2@s.whatsapp.net", got[0].JID)

	// substring match across multiple rows + limit
	got, err = cs.Search(ctx, "o", 2)
	require.NoError(t, err)
	require.LessOrEqual(t, len(got), 2)

	// no matches
	got, err = cs.Search(ctx, "zzzz", 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}
```

- [ ] **Step 3: Run failing tests**

```bash
go test ./internal/store/sqlite/... -run 'Count|TotalUnread|Search'
```

Expected: FAIL — methods undefined.

- [ ] **Step 4: Implement Count + TotalUnread on ChatStore**

Append to `internal/store/sqlite/chats.go`:
```go
func (s *ChatStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&n); err != nil {
		return 0, fmt.Errorf("chats count: %w", err)
	}
	return n, nil
}

func (s *ChatStore) TotalUnread(ctx context.Context) (int, error) {
	var n sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(unread_count), 0) FROM chats`).Scan(&n); err != nil {
		return 0, fmt.Errorf("chats total_unread: %w", err)
	}
	return int(n.Int64), nil
}
```

- [ ] **Step 5: Implement Count on MessageStore**

Append to `internal/store/sqlite/messages.go`:
```go
func (s *MessageStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("messages count: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 6: Implement Search + Count on ContactStore**

Append to `internal/store/sqlite/contacts.go`:
```go
func (s *ContactStore) Search(ctx context.Context, query string, limit int) ([]store.Contact, error) {
	pat := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+contactColumns+` FROM contacts
		WHERE lower(coalesce(push_name, '')) LIKE ?
		   OR lower(coalesce(full_name, '')) LIKE ?
		   OR lower(coalesce(business_name, '')) LIKE ?
		ORDER BY jid ASC
		LIMIT ?
	`, pat, pat, pat, limit)
	if err != nil {
		return nil, fmt.Errorf("contacts search: %w", err)
	}
	defer rows.Close()
	var out []store.Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, fmt.Errorf("contacts search scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *ContactStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&n); err != nil {
		return 0, fmt.Errorf("contacts count: %w", err)
	}
	return n, nil
}
```

Add `"strings"` to the imports of `contacts.go` if not already present.

- [ ] **Step 7: Run all tests**

```bash
go test ./internal/store/sqlite/... -v
```

Expected: all PASS.

Service tests will fail to compile because the `inMemoryBundle` fakes don't yet implement the new interface methods. We need to extend them now to keep the build green.

- [ ] **Step 8: Extend the in-memory fakes**

Edit `internal/service/service_test.go`. Find each fake store and add the new methods:

```go
// chatStore
func (s *chatStore) Count(context.Context) (int, error) { return len(s.m), nil }
func (s *chatStore) TotalUnread(context.Context) (int, error) {
	total := 0
	for _, c := range s.m {
		total += c.UnreadCount
	}
	return total, nil
}

// messageStore
func (s *messageStore) Count(context.Context) (int, error) {
	n := 0
	for _, m := range s.m {
		if m.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

// contactStore
func (s *contactStore) Count(context.Context) (int, error) { return len(s.m), nil }
func (s *contactStore) Search(_ context.Context, query string, limit int) ([]store.Contact, error) {
	q := strings.ToLower(query)
	var out []store.Contact
	for _, c := range s.m {
		if strings.Contains(strings.ToLower(c.PushName), q) ||
			strings.Contains(strings.ToLower(c.FullName), q) ||
			strings.Contains(strings.ToLower(c.BusinessName), q) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

`strings` and `sort` should already be imported from Task 1.

Same for `failingMessageStore` (Plan 04 added it for one test) — it also satisfies `MessageStore`, so it needs Count too:
```go
// failingMessageStore (Plan 04 helper) — extend
func (f *failingMessageStore) Count(context.Context) (int, error) { return 0, nil }
```

- [ ] **Step 9: Run full test suite**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/store/store.go internal/store/sqlite/ internal/service/service_test.go
git commit -m "store: Count + TotalUnread + ContactStore.Search"
```

---

## Task 3: Service.ListChats + GetChat + ListMessages

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestListChatsValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	// limit < 1
	_, err := s.ListChats(context.Background(), time.Time{}, 0, false)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit > 100
	_, err = s.ListChats(context.Background(), time.Time{}, 101, false)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListChatsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	(*chats)["a@s.whatsapp.net"] = store.Chat{JID: "a@s.whatsapp.net", Kind: "user", LastMsgAt: time.Unix(100, 0).UTC()}
	(*chats)["b@s.whatsapp.net"] = store.Chat{JID: "b@s.whatsapp.net", Kind: "user", LastMsgAt: time.Unix(200, 0).UTC()}

	got, err := s.ListChats(ctx, time.Time{}, 50, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// in-memory fake sorts by LastMsgAt DESC
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
}

func TestGetChatNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)
	_, err := s.GetChat(context.Background(), "missing@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetChatHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)
	(*chats)["x@s.whatsapp.net"] = store.Chat{JID: "x@s.whatsapp.net", Name: "X", Kind: "user"}

	got, err := s.GetChat(ctx, "x@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, "X", got.Name)
}

func TestListMessagesValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	// empty chat_jid
	_, err := s.ListMessages(context.Background(), "", time.Time{}, 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit out of range
	_, err = s.ListMessages(context.Background(), "x@s.whatsapp.net", time.Time{}, 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.ListMessages(context.Background(), "x@s.whatsapp.net", time.Time{}, 101)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListMessagesHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)
	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "x@s.whatsapp.net", Timestamp: time.Unix(100, 0).UTC(), Kind: "text", Body: "hi"}

	got, err := s.ListMessages(ctx, "x@s.whatsapp.net", time.Time{}, 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "M1", got[0].ID)
}
```

The `messageStore.ListByChat` in-memory fake currently returns `nil, nil`. Update it now to honor the basic contract (filter by chat_jid, exclude deleted, sort by ts DESC, limit):

Find:
```go
func (s *messageStore) ListByChat(context.Context, string, int, time.Time) ([]store.Message, error) {
	return nil, nil
}
```

Replace with:
```go
func (s *messageStore) ListByChat(_ context.Context, chatJID string, limit int, beforeTS time.Time) ([]store.Message, error) {
	var out []store.Message
	for _, m := range s.m {
		if m.ChatJID != chatJID {
			continue
		}
		if m.DeletedAt != nil {
			continue
		}
		if !beforeTS.IsZero() && !m.Timestamp.Before(beforeTS) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestListChats|TestGetChat|TestListMessages'
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement ListChats / GetChat / ListMessages on Service**

Edit `internal/service/service.go`. Extend the `Service` interface:
```go
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error

	SendText(ctx context.Context, chatJID, text string) (store.Message, error)

	// Plan 05
	ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error)
	GetChat(ctx context.Context, jid string) (store.Chat, error)
	ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error)
}
```

Add the limit-clamp helper near `ErrInvalidRequest`:
```go
const (
	maxTextLen = 4096
	minLimit   = 1
	maxLimit   = 100
)

func validateLimit(limit int) error {
	if limit < minLimit || limit > maxLimit {
		return fmt.Errorf("%w: limit must be in [%d, %d]", ErrInvalidRequest, minLimit, maxLimit)
	}
	return nil
}
```

Add the three methods at the bottom of the file:
```go
func (s *svc) ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Chats.List(ctx, beforeMsgAt, limit, includeArchived)
}

func (s *svc) GetChat(ctx context.Context, jid string) (store.Chat, error) {
	if strings.TrimSpace(jid) == "" {
		return store.Chat{}, fmt.Errorf("%w: jid is required", ErrInvalidRequest)
	}
	return s.bundle.Chats.Get(ctx, jid)
}

func (s *svc) ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error) {
	if strings.TrimSpace(chatJID) == "" {
		return nil, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Messages.ListByChat(ctx, chatJID, limit, beforeTS)
}
```

Note the parameter order on `ListByChat` — Plan 03's signature is `(ctx, chatJID, limit, beforeTS)`. The Service method takes them in `(chatJID, beforeTS, limit)` order to match the HTTP query param convention `?before=...&limit=...`. The translation happens at the call site.

Add `"strings"` and `"time"` to imports of `service.go` if not already present.

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/service/... -v
```

Expected: all PASS — Plan 02/04 tests + 6 new chat/message tests.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: ListChats + GetChat + ListMessages with limit clamp"
```

---

## Task 4: Service.SearchMessages + ListContacts + SearchContacts

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestSearchMessagesValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	// empty q
	_, err := s.SearchMessages(context.Background(), "", 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit out of range
	_, err = s.SearchMessages(context.Background(), "x", 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.SearchMessages(context.Background(), "x", 101)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListContactsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}

	got, err := s.ListContacts(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSearchContactsValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	_, err := s.SearchContacts(context.Background(), "", 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.SearchContacts(context.Background(), "x", 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSearchContactsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net", PushName: "Alice"}
	(*contacts)["b@s.whatsapp.net"] = store.Contact{JID: "b@s.whatsapp.net", PushName: "Bob"}

	got, err := s.SearchContacts(context.Background(), "ali", 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)
}
```

The in-memory `contactStore.List` currently returns `nil, nil`. Update it:
Find:
```go
func (s *contactStore) List(context.Context) ([]store.Contact, error) { return nil, nil }
```

Replace with:
```go
func (s *contactStore) List(_ context.Context) ([]store.Contact, error) {
	var out []store.Contact
	for _, c := range s.m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	return out, nil
}
```

The in-memory `messageStore.Search` currently returns `nil, nil`. Update it:
Find:
```go
func (s *messageStore) Search(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
```

Replace with:
```go
func (s *messageStore) Search(_ context.Context, query string, limit int) ([]store.Message, error) {
	q := strings.ToLower(query)
	var out []store.Message
	for _, m := range s.m {
		if m.DeletedAt != nil {
			continue
		}
		if strings.Contains(strings.ToLower(m.Body), q) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

Same for `failingMessageStore.Search` (Plan 04 helper):
Find:
```go
func (f *failingMessageStore) Search(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
```

Leave it as `nil, nil` — it's only used in `TestSendTextPersistFailureStillSucceeds`, which doesn't exercise search.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/service/... -run 'TestSearchMessages|TestListContacts|TestSearchContacts'
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement on Service**

Extend the `Service` interface:
```go
SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error)
ListContacts(ctx context.Context) ([]store.Contact, error)
SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error)
```

Append to `service.go`:
```go
func (s *svc) SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: q is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Messages.Search(ctx, query, limit)
}

func (s *svc) ListContacts(ctx context.Context) ([]store.Contact, error) {
	return s.bundle.Contacts.List(ctx)
}

func (s *svc) SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: q is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Contacts.Search(ctx, query, limit)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: SearchMessages + ListContacts + SearchContacts"
```

---

## Task 5: Service.Stats

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/service/service_test.go`:
```go
func TestStats(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, nil)

	(*chats)["a@s.whatsapp.net"] = store.Chat{JID: "a@s.whatsapp.net", UnreadCount: 3}
	(*chats)["b@s.whatsapp.net"] = store.Chat{JID: "b@s.whatsapp.net", UnreadCount: 1}
	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "a@s.whatsapp.net", Body: "x"}
	(*msgs)["M2"] = store.Message{ID: "M2", ChatJID: "a@s.whatsapp.net", Body: "y"}
	(*msgs)["M3"] = store.Message{ID: "M3", ChatJID: "b@s.whatsapp.net", Body: "z"}
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net"}

	got, err := s.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Chats)
	assert.Equal(t, 3, got.Messages)
	assert.Equal(t, 1, got.Contacts)
	assert.Equal(t, 4, got.UnreadTotal)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/service/... -run TestStats
```

Expected: FAIL — `service.Stats` and `(*svc).Stats` undefined.

- [ ] **Step 3: Implement Stats**

Edit `internal/service/service.go`. Add the type after the `ErrInvalidRequest` block:
```go
type Stats struct {
	Chats       int `json:"chats"`
	Messages    int `json:"messages"`
	Contacts    int `json:"contacts"`
	UnreadTotal int `json:"unread_total"`
}
```

Extend the interface:
```go
Stats(ctx context.Context) (Stats, error)
```

Append the method:
```go
func (s *svc) Stats(ctx context.Context) (Stats, error) {
	chatsCount, err := s.bundle.Chats.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats chats: %w", err)
	}
	msgsCount, err := s.bundle.Messages.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats messages: %w", err)
	}
	contactsCount, err := s.bundle.Contacts.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats contacts: %w", err)
	}
	unread, err := s.bundle.Chats.TotalUnread(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats unread: %w", err)
	}
	return Stats{
		Chats:       chatsCount,
		Messages:    msgsCount,
		Contacts:    contactsCount,
		UnreadTotal: unread,
	}, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: Stats composes counts + total unread"
```

---

## Task 6: HTTP chats handlers (3 handlers, 3 routes)

**Files:**
- Create: `internal/transport/http/chats.go`
- Create: `internal/transport/http/chats_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/chats_test.go`:
```go
package http_test

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeChatsSvc struct {
	listResp     []store.Chat
	listErr      error
	gotBefore    time.Time
	gotLimit     int
	gotInclArch  bool

	getResp store.Chat
	getErr  error
	gotJID  string

	listMsgsResp     []store.Message
	listMsgsErr      error
	gotMsgsChatJID   string
	gotMsgsBefore    time.Time
	gotMsgsLimit     int
}

func (f *fakeChatsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeChatsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f *fakeChatsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f *fakeChatsSvc) Logout(context.Context) error                                          { return nil }
func (f *fakeChatsSvc) SendText(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeChatsSvc) ListChats(_ context.Context, before time.Time, limit int, inclArch bool) ([]store.Chat, error) {
	f.gotBefore = before
	f.gotLimit = limit
	f.gotInclArch = inclArch
	return f.listResp, f.listErr
}
func (f *fakeChatsSvc) GetChat(_ context.Context, jid string) (store.Chat, error) {
	f.gotJID = jid
	return f.getResp, f.getErr
}
func (f *fakeChatsSvc) ListMessages(_ context.Context, chatJID string, before time.Time, limit int) ([]store.Message, error) {
	f.gotMsgsChatJID = chatJID
	f.gotMsgsBefore = before
	f.gotMsgsLimit = limit
	return f.listMsgsResp, f.listMsgsErr
}
func (f *fakeChatsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeChatsSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeChatsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeChatsSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

var _ service.Service = (*fakeChatsSvc)(nil)

func TestListChatsHappyPath(t *testing.T) {
	f := &fakeChatsSvc{listResp: []store.Chat{
		{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
	}}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?limit=10&before=2026-05-02T00:00:00Z&include_archived=true")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Chats []map[string]any `json:"chats"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Chats, 1)
	assert.Equal(t, "a@s.whatsapp.net", body.Chats[0]["jid"])

	assert.Equal(t, 10, f.gotLimit)
	assert.True(t, f.gotInclArch)
	assert.False(t, f.gotBefore.IsZero())
}

func TestListChatsDefaults(t *testing.T) {
	f := &fakeChatsSvc{}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 50, f.gotLimit)
	assert.False(t, f.gotInclArch)
	assert.True(t, f.gotBefore.IsZero())
}

func TestListChatsValidation(t *testing.T) {
	f := &fakeChatsSvc{}
	srv := httptest.NewServer(httpapi.ListChatsHandler(f))
	defer srv.Close()

	cases := []struct{ q, label string }{
		{"limit=foo", "non-int limit"},
		{"limit=0", "limit too small"},
		{"limit=101", "limit too large"},
		{"before=garbage", "bad timestamp"},
		{"include_archived=maybe", "bad bool"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + tc.q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

func TestGetChatHappyPath(t *testing.T) {
	f := &fakeChatsSvc{getResp: store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user"}}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "a@s.whatsapp.net", f.gotJID)
}

func TestGetChatNotFound(t *testing.T) {
	f := &fakeChatsSvc{getErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/missing@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestGetChatInternalError(t *testing.T) {
	f := &fakeChatsSvc{getErr: errors.New("boom")}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}", httpapi.GetChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

func TestListMessagesByChatHappyPath(t *testing.T) {
	f := &fakeChatsSvc{listMsgsResp: []store.Message{
		{ID: "M1", ChatJID: "a@s.whatsapp.net", Body: "hi", Timestamp: time.Now()},
	}}
	r := chi.NewRouter()
	r.Get("/v1/chats/{jid}/messages", httpapi.ListMessagesByChatHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/chats/a@s.whatsapp.net/messages?limit=20")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "a@s.whatsapp.net", f.gotMsgsChatJID)
	assert.Equal(t, 20, f.gotMsgsLimit)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/transport/http/... -run 'TestListChats|TestGetChat|TestListMessagesByChat'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/chats.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/go-chi/chi/v5"
)

const (
	defaultLimit = 50
	maxAPILimit  = 100
)

// parseLimit reads ?limit=N (default 50, range [1, 100]).
func parseLimit(r *http.Request) (int, error) {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 || n > maxAPILimit {
		return 0, errors.New("limit must be in [1, 100]")
	}
	return n, nil
}

// parseBefore reads ?before=<RFC3339>; absent or empty → zero time (no cursor).
func parseBefore(r *http.Request) (time.Time, error) {
	q := r.URL.Query().Get("before")
	if q == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, q)
	if err != nil {
		return time.Time{}, errors.New("before must be RFC 3339 timestamp")
	}
	return t, nil
}

// parseIncludeArchived reads ?include_archived=<bool>; default false.
func parseIncludeArchived(r *http.Request) (bool, error) {
	q := r.URL.Query().Get("include_archived")
	if q == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(q)
	if err != nil {
		return false, errors.New("include_archived must be true or false")
	}
	return b, nil
}

// ListChatsHandler handles GET /v1/chats.
func ListChatsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		before, err := parseBefore(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		inclArch, err := parseIncludeArchived(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		chats, err := svc.ListChats(r.Context(), before, limit, inclArch)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chats": encodeChats(chats)})
	})
}

// GetChatHandler handles GET /v1/chats/{jid}.
func GetChatHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jid := chi.URLParam(r, "jid")
		c, err := svc.GetChat(r.Context(), jid)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, encodeChat(c))
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "chat.not_found", "no chat with that jid")
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
		}
	})
}

// ListMessagesByChatHandler handles GET /v1/chats/{jid}/messages.
func ListMessagesByChatHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jid := chi.URLParam(r, "jid")
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		before, err := parseBefore(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		msgs, err := svc.ListMessages(r.Context(), jid, before, limit)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": encodeMessages(msgs)})
	})
}

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func encodeChat(c store.Chat) map[string]any {
	return map[string]any{
		"jid":          c.JID,
		"name":         nilOrString(strPtr(c.Name)),
		"kind":         c.Kind,
		"last_msg_at":  nilOrTime(timePtr(c.LastMsgAt)),
		"unread_count": c.UnreadCount,
		"archived":     c.Archived,
	}
}

func encodeChats(chats []store.Chat) []map[string]any {
	out := make([]map[string]any, 0, len(chats))
	for _, c := range chats {
		out = append(out, encodeChat(c))
	}
	return out
}

func encodeMessage(m store.Message) map[string]any {
	return map[string]any{
		"id":         m.ID,
		"chat_jid":   m.ChatJID,
		"sender_jid": m.SenderJID,
		"ts":         m.Timestamp.UTC().Format(time.RFC3339),
		"kind":       m.Kind,
		"body":       m.Body,
		"reply_to":   nilOrString(strPtr(m.ReplyTo)),
		"edited_at":  nilOrTime(m.EditedAt),
		"deleted_at": nilOrTime(m.DeletedAt),
	}
}

func encodeMessages(msgs []store.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, encodeMessage(m))
	}
	return out
}

// strPtr returns nil if s is empty, else &s — keeps "" out of the JSON shape.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timePtr returns nil if t is zero, else &t — keeps the zero time out of JSON.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
```

This file uses `nilOrString` and `nilOrTime` already defined in `status.go` (Plan 02). No duplication.

- [ ] **Step 4: Wire the routes**

Edit `internal/transport/http/router.go`. In the auth-protected group, after the existing routes, append:
```go
r.Method(http.MethodGet, "/chats", ListChatsHandler(d.Service))
r.Method(http.MethodGet, "/chats/{jid}", GetChatHandler(d.Service))
r.Method(http.MethodGet, "/chats/{jid}/messages", ListMessagesByChatHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/transport/http/... -v
```

Expected: all PASS — existing tests + 7 new chats tests.

You'll likely need to update the existing fake services in `status_test.go`, `login_qr_test.go`, `login_phone_test.go`, `logout_test.go`, `messages_test.go` — they all have `var _ service.Service = ...` checks that now require the new Plan 05 methods. Add stub methods to each:

For each existing fake (fakeStatusSvc, fakeLoginQRSvc, fakeLoginPhoneSvc, fakeLogoutSvc, fakeSendSvc — five total, one per test file):
```go
func (f fakeStatusSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f fakeStatusSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f fakeStatusSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatusSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatusSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f fakeStatusSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f fakeStatusSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }
```

Adapt the receiver name (`fakeStatusSvc`, `fakeLoginQRSvc`, `fakeLoginPhoneSvc`, `fakeLogoutSvc`, `fakeSendSvc`) and the receiver kind (value vs pointer — match the existing methods in each fake) per file. Add `"time"`, `"github.com/askarzh/whatsmeow-api/internal/store"`, and `"github.com/askarzh/whatsmeow-api/internal/service"` imports as needed (most are already present).

This is mechanical bridging work — exactly what Plan 04's Task 5 had to do for SendText. The pattern is identical.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/chats.go internal/transport/http/chats_test.go internal/transport/http/router.go internal/transport/http/{status,login_qr,login_phone,logout,messages}_test.go
git commit -m "http: GET /v1/chats + /v1/chats/{jid} + /v1/chats/{jid}/messages"
```

---

## Task 7: HTTP messages search handler

**Files:**
- Modify: `internal/transport/http/messages.go`
- Modify: `internal/transport/http/messages_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Extend fakeSendSvc to capture search calls**

In `internal/transport/http/messages_test.go`, find the `fakeSendSvc` struct definition (Plan 04 added it) and add capture fields. Also replace the no-op `SearchMessages` stub from Task 6's bridge fix with a capturing version:

```go
type fakeSendSvc struct {
	resp store.Message
	err  error

	gotChat string
	gotText string

	// Plan 05 search capture
	searchResp     []store.Message
	searchErr      error
	gotSearchQ     string
	gotSearchLimit int
}

// existing SendText, plus the Plan 05 stubs that were added in Task 6's bridge fix.
// Replace SearchMessages with a capturing impl:
func (f *fakeSendSvc) SearchMessages(_ context.Context, q string, limit int) ([]store.Message, error) {
	f.gotSearchQ = q
	f.gotSearchLimit = limit
	return f.searchResp, f.searchErr
}
```

- [ ] **Step 2: Append the failing tests**

Append to `internal/transport/http/messages_test.go`:
```go
func TestSearchMessagesHappyPath(t *testing.T) {
	f := &fakeSendSvc{searchResp: []store.Message{
		{ID: "M1", ChatJID: "c@s.whatsapp.net", Body: "fox", Timestamp: time.Now()},
	}}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=fox&limit=20")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Messages, 1)
	assert.Equal(t, "M1", body.Messages[0]["id"])
	assert.Equal(t, "fox", f.gotSearchQ)
	assert.Equal(t, 20, f.gotSearchLimit)
}

func TestSearchMessagesDefaultLimit(t *testing.T) {
	f := &fakeSendSvc{}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=anything")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 50, f.gotSearchLimit)
}

func TestSearchMessagesValidation(t *testing.T) {
	f := &fakeSendSvc{}
	srv := httptest.NewServer(httpapi.SearchMessagesHandler(f))
	defer srv.Close()

	cases := []string{"", "limit=0", "q=x&limit=foo", "q=x&limit=101"}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}
```

Add `encoding/json` and `time` to the imports of `messages_test.go` if not already present.

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/transport/http/... -run TestSearchMessages
```

Expected: FAIL — handler undefined.

- [ ] **Step 4: Implement the handler**

Append to `internal/transport/http/messages.go`:
```go
// SearchMessagesHandler handles GET /v1/messages/search?q=...&limit=...
func SearchMessagesHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "q is required")
			return
		}
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		msgs, err := svc.SearchMessages(r.Context(), q, limit)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": encodeMessages(msgs)})
	})
}
```

`parseLimit`, `writeJSON`, `encodeMessages` are defined in `chats.go` (Task 6) — same package, accessible.

- [ ] **Step 5: Wire the route**

Edit `internal/transport/http/router.go`. Append in the auth-protected group:
```go
r.Method(http.MethodGet, "/messages/search", SearchMessagesHandler(d.Service))
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/transport/http/... -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/http/messages.go internal/transport/http/messages_test.go internal/transport/http/router.go
git commit -m "http: GET /v1/messages/search"
```

---

## Task 8: HTTP contacts + stats handlers

**Files:**
- Create: `internal/transport/http/contacts.go`
- Create: `internal/transport/http/contacts_test.go`
- Create: `internal/transport/http/stats.go`
- Create: `internal/transport/http/stats_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/contacts_test.go`:
```go
package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeContactsSvc struct {
	listResp     []store.Contact
	searchResp   []store.Contact
	searchErr    error
	gotSearchQ   string
	gotSearchLim int
}

func (f *fakeContactsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeContactsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeContactsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeContactsSvc) Logout(context.Context) error { return nil }
func (f *fakeContactsSvc) SendText(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeContactsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeContactsSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeContactsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeContactsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeContactsSvc) ListContacts(context.Context) ([]store.Contact, error) {
	return f.listResp, nil
}
func (f *fakeContactsSvc) SearchContacts(_ context.Context, q string, limit int) ([]store.Contact, error) {
	f.gotSearchQ = q
	f.gotSearchLim = limit
	return f.searchResp, f.searchErr
}
func (f *fakeContactsSvc) Stats(context.Context) (service.Stats, error) { return service.Stats{}, nil }

var _ service.Service = (*fakeContactsSvc)(nil)

func TestListContactsHappyPath(t *testing.T) {
	f := &fakeContactsSvc{listResp: []store.Contact{{JID: "a@s.whatsapp.net", PushName: "A"}}}
	srv := httptest.NewServer(httpapi.ListContactsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Contacts []map[string]any `json:"contacts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Contacts, 1)
	assert.Equal(t, "a@s.whatsapp.net", body.Contacts[0]["jid"])
}

func TestSearchContactsHappyPath(t *testing.T) {
	f := &fakeContactsSvc{searchResp: []store.Contact{{JID: "a@s.whatsapp.net", PushName: "Alice"}}}
	srv := httptest.NewServer(httpapi.SearchContactsHandler(f))
	defer srv.Close()

	res, err := http.Get(srv.URL + "?q=ali&limit=10")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Contacts []map[string]any `json:"contacts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Contacts, 1)
	assert.Equal(t, "ali", f.gotSearchQ)
	assert.Equal(t, 10, f.gotSearchLim)
}

func TestSearchContactsValidation(t *testing.T) {
	f := &fakeContactsSvc{}
	srv := httptest.NewServer(httpapi.SearchContactsHandler(f))
	defer srv.Close()

	for _, q := range []string{"", "limit=0", "q=x&limit=101"} {
		t.Run(q, func(t *testing.T) {
			res, err := http.Get(srv.URL + "?" + q)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}
```

Add `"time"` to the imports.

Create `internal/transport/http/stats_test.go`:
```go
package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStatsSvc struct{ resp service.Stats }

func (f fakeStatsSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f fakeStatsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f fakeStatsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f fakeStatsSvc) Logout(context.Context) error                                          { return nil }
func (f fakeStatsSvc) SendText(context.Context, string, string) (store.Message, error)        { return store.Message{}, nil }
func (f fakeStatsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error)  { return nil, nil }
func (f fakeStatsSvc) GetChat(context.Context, string) (store.Chat, error)                    { return store.Chat{}, nil }
func (f fakeStatsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f fakeStatsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error)   { return nil, nil }
func (f fakeStatsSvc) ListContacts(context.Context) ([]store.Contact, error)                  { return nil, nil }
func (f fakeStatsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error)   { return nil, nil }
func (f fakeStatsSvc) Stats(context.Context) (service.Stats, error)                           { return f.resp, nil }

var _ service.Service = fakeStatsSvc{}

func TestStatsHappyPath(t *testing.T) {
	srv := httptest.NewServer(httpapi.StatsHandler(fakeStatsSvc{
		resp: service.Stats{Chats: 12, Messages: 4567, Contacts: 8, UnreadTotal: 3},
	}))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Chats       int `json:"chats"`
		Messages    int `json:"messages"`
		Contacts    int `json:"contacts"`
		UnreadTotal int `json:"unread_total"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, 12, body.Chats)
	assert.Equal(t, 4567, body.Messages)
	assert.Equal(t, 8, body.Contacts)
	assert.Equal(t, 3, body.UnreadTotal)
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/transport/http/... -run 'TestListContacts|TestSearchContacts|TestStats'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/contacts.go`:
```go
package http

import (
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

// ListContactsHandler handles GET /v1/contacts (no pagination — returns all).
func ListContactsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacts, err := svc.ListContacts(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contacts": encodeContacts(contacts)})
	})
}

// SearchContactsHandler handles GET /v1/contacts/search?q=...&limit=...
func SearchContactsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "q is required")
			return
		}
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		contacts, err := svc.SearchContacts(r.Context(), q, limit)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contacts": encodeContacts(contacts)})
	})
}

func encodeContact(c store.Contact) map[string]any {
	return map[string]any{
		"jid":           c.JID,
		"push_name":     nilOrString(strPtr(c.PushName)),
		"full_name":     nilOrString(strPtr(c.FullName)),
		"business_name": nilOrString(strPtr(c.BusinessName)),
	}
}

func encodeContacts(cs []store.Contact) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, encodeContact(c))
	}
	return out
}
```

Create `internal/transport/http/stats.go`:
```go
package http

import (
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// StatsHandler handles GET /v1/stats.
func StatsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := svc.Stats(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
}
```

- [ ] **Step 4: Wire the routes**

Edit `internal/transport/http/router.go`. Append in the auth-protected group:
```go
r.Method(http.MethodGet, "/contacts", ListContactsHandler(d.Service))
r.Method(http.MethodGet, "/contacts/search", SearchContactsHandler(d.Service))
r.Method(http.MethodGet, "/stats", StatsHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/contacts.go internal/transport/http/contacts_test.go internal/transport/http/stats.go internal/transport/http/stats_test.go internal/transport/http/router.go
git commit -m "http: GET /v1/contacts + /v1/contacts/search + /v1/stats"
```

---

## Task 9: End-to-end smoke test

**Files:** none modified.

This task verifies all seven new endpoints respond correctly. It does NOT require a paired account — the daemon will boot with empty tables and we can still exercise validation and empty-result paths.

- [ ] **Step 1: Build and start the daemon**

```bash
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
```

Expected: `app store opened` log line, `server starting`, no errors.

- [ ] **Step 2: Verify happy paths against an empty DB**

```bash
curl -s http://127.0.0.1:8080/v1/chats
# Expected: {"chats":[]}

curl -s http://127.0.0.1:8080/v1/contacts
# Expected: {"contacts":[]}

curl -s http://127.0.0.1:8080/v1/stats
# Expected: {"chats":0,"messages":0,"contacts":0,"unread_total":0}
```

- [ ] **Step 3: Verify pagination defaults**

```bash
curl -s 'http://127.0.0.1:8080/v1/chats?limit=10'
# Expected: {"chats":[]} (still empty, but limit accepted)

curl -i 'http://127.0.0.1:8080/v1/chats?limit=foo'
# Expected: HTTP/1.1 400, "request.invalid"

curl -i 'http://127.0.0.1:8080/v1/chats?limit=200'
# Expected: 400

curl -i 'http://127.0.0.1:8080/v1/chats?before=garbage'
# Expected: 400

curl -i 'http://127.0.0.1:8080/v1/chats?include_archived=maybe'
# Expected: 400
```

- [ ] **Step 4: Verify search validation**

```bash
curl -i 'http://127.0.0.1:8080/v1/messages/search'
# Expected: 400, "q is required"

curl -i 'http://127.0.0.1:8080/v1/contacts/search?q='
# Expected: 400

curl -s 'http://127.0.0.1:8080/v1/messages/search?q=anything'
# Expected: {"messages":[]}
```

- [ ] **Step 5: Verify chat-not-found**

```bash
curl -i 'http://127.0.0.1:8080/v1/chats/missing@s.whatsapp.net'
# Expected: 404, "chat.not_found"
```

- [ ] **Step 6: Verify chat messages route exists (empty result)**

```bash
curl -s 'http://127.0.0.1:8080/v1/chats/anything@s.whatsapp.net/messages'
# Expected: {"messages":[]}
```

- [ ] **Step 7: Stop the daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -5 /tmp/wmapi.log
```

Expected last log line: `... msg="server stopped"`.

- [ ] **Step 8: Mark this task done**

No commit — code unchanged.

If you have a paired account from Plan 02, you can additionally exchange a few messages and re-run Steps 2-6 to see real data flow through. Optional.

---

## Task 10: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status section**

Edit `README.md`. Replace the existing Status block:
```markdown
- **Plan 04 (send + receive)** shipped: `POST /v1/messages` sends a text message via whatsmeow and persists the outbound row. Inbound message events from whatsmeow are persisted automatically (text + media kinds; media metadata lands in Plan 06). `chats.last_msg_at`, `chats.unread_count`, and `contacts.push_name` update in real time.

Listing / searching the persisted messages lands in Plan 05; reactions / replies / edits / deletes / read receipts in Plan 07.
```

…with:
```markdown
- **Plan 04 (send + receive)** shipped: `POST /v1/messages` sends a text message via whatsmeow and persists the outbound row. Inbound message events from whatsmeow are persisted automatically (text + media kinds; media metadata lands in Plan 06). `chats.last_msg_at`, `chats.unread_count`, and `contacts.push_name` update in real time.
- **Plan 05 (list + search)** shipped: read-side endpoints over the app store. `GET /v1/chats` (cursor pagination), `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages` (cursor pagination), `GET /v1/messages/search?q=`, `GET /v1/contacts`, `GET /v1/contacts/search?q=`, `GET /v1/stats`.

Reactions / replies / edits / deletes / read receipts land in Plan 07; media in Plan 06; SSE event stream in Plan 09.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 05"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` all PASS
- [ ] Manual smoke (Task 9) all green: empty-DB happy paths return `[]`, validation paths return 400, unknown chat returns 404
- [ ] (Optional, with a paired account) populated DB returns realistic data on each new endpoint
- [ ] `git log --oneline` shows ~10 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 06 — media** (or whichever milestone is next).

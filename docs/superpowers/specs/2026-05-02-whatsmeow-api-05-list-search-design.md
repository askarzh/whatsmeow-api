# whatsmeow-api Plan 05 — List + Search Design

**Date:** 2026-05-02
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 04 (send + receive + persist) — merged.

## 1. Purpose

Read-side endpoints over the app store populated in Plan 04. Plan 05 ships seven new HTTP endpoints — chat list / chat detail / messages-in-chat / message search / contact list / contact search / stats — without altering write paths. After Plan 05, an HTTP client can both drive a chat (via Plan 04's `POST /v1/messages`) and reflect the full state.

## 2. Goals

- All seven endpoints land at once. Pagination uses cursor-by-timestamp (matching the `MessageStore.ListByChat` signature already shipped in Plan 03). Contacts return all rows in one response (small table; no pagination).
- Search returns flat `[]Message` / `[]Contact` arrays. No grouping by chat, no embedded chat names. The client looks up chat metadata via separate `/v1/chats/{jid}` calls if it wants display labels.
- The store interface gains `Count` methods on Chat / Message / Contact and a `TotalUnread` on Chat. The service layer's `Stats` method composes them.
- Validation lives at both layers (HTTP handler clamps `limit` and parses `before`; service rejects out-of-range values). HTTP-side validation is the primary gate; service-side validation defends against direct in-process callers (Plan 09 SSE may dispatch through service directly).

## 3. Non-goals (Plan 05)

- Read-receipts / `unread_count` decrement → Plan 07.
- Filtering messages by `kind` (image/video/etc.) → defer until a consumer asks.
- Sorting options on `/v1/contacts` (alphabetical-by-JID is the only order).
- Media downloads (`GET /v1/media/{message_id}`) → Plan 06.
- Group membership endpoints (`/v1/groups/...`) → Plan 08.
- FTS / specialized index for contacts. Plan 05 uses `LIKE '%q%'` on `lower(push_name) | lower(full_name) | lower(business_name)`. Add an FTS5 contacts index when a real consumer hits a slow-search complaint.

## 4. Architecture

```
HTTP                                  Service                              Store
GET /v1/chats                         ListChats(beforeTS,limit,inclArch)   ChatStore.List
GET /v1/chats/{jid}                   GetChat(jid)                         ChatStore.Get
GET /v1/chats/{jid}/messages          ListMessages(jid,beforeTS,limit)     MessageStore.ListByChat
GET /v1/messages/search?q=&limit=     SearchMessages(q,limit)              MessageStore.Search
GET /v1/contacts                      ListContacts()                       ContactStore.List
GET /v1/contacts/search?q=&limit=     SearchContacts(q,limit)              ContactStore.Search (NEW)
GET /v1/stats                         Stats() → {Chats,Msgs,Contacts,Unr}  ChatStore.Count + .TotalUnread
                                                                           MessageStore.Count
                                                                           ContactStore.Count
```

All handlers are read-only. All routes register in the existing auth-protected group (next to the Plan 04 `POST /v1/messages`). No new SSE.

## 5. Store interface changes

```go
// internal/store/store.go

type ChatStore interface {
    Put(ctx, c) error
    Get(ctx, jid) (Chat, error)

    // CHANGED in Plan 05: cursor pagination + includeArchived
    List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]Chat, error)

    SetArchived(ctx, jid, archived) error

    // NEW
    Count(ctx context.Context) (int, error)
    TotalUnread(ctx context.Context) (int, error)
}

type MessageStore interface {
    Put / Get / ListByChat / Search / SoftDelete (unchanged)
    Count(ctx context.Context) (int, error) // NEW
}

type ContactStore interface {
    Put / Get / List (unchanged)
    Search(ctx context.Context, query string, limit int) ([]Contact, error) // NEW
    Count(ctx context.Context) (int, error)                                  // NEW
}

type EventsLog (unchanged)
type KV       (unchanged)
```

`Bundle` is unchanged — its fields are still typed as the (extended) interfaces.

`ChatStore.List` migration: Plan 03's signature was `(ctx, includeArchived bool)`. Plan 05 adds the `beforeMsgAt time.Time` and `limit int` parameters BEFORE `includeArchived`. Two existing call sites get migrated:
- `internal/store/sqlite/chats_test.go` — its existing `TestChatList` already populates rows with various `last_msg_at` values; the test gets rewritten to also pass `time.Time{}` (zero, meaning "from newest") and `1000` (a high limit).
- `internal/service/service_test.go` — the `chatStore` test fake's `List` method needs the new signature; behavior continues to ignore the cursor for simplicity (the in-memory bundle isn't expected to validate pagination math).

## 6. SQLite implementation changes

`internal/store/sqlite/chats.go`:

```go
func (s *ChatStore) List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
    q := `SELECT ` + chatColumns + ` FROM chats`
    var conds []string
    var args []any
    if !includeArchived {
        conds = append(conds, `archived = 0`)
    }
    if !beforeMsgAt.IsZero() {
        conds = append(conds, `last_msg_at IS NOT NULL AND last_msg_at < ?`)
        args = append(args, beforeMsgAt.Unix())
    }
    if len(conds) > 0 {
        q += ` WHERE ` + strings.Join(conds, ` AND `)
    }
    q += ` ORDER BY last_msg_at DESC NULLS LAST, jid ASC LIMIT ?`
    args = append(args, limit)
    // ... QueryContext + scan loop ...
}

func (s *ChatStore) Count(ctx context.Context) (int, error) {
    var n int
    err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&n)
    if err != nil { return 0, fmt.Errorf("chats count: %w", err) }
    return n, nil
}

func (s *ChatStore) TotalUnread(ctx context.Context) (int, error) {
    var n sql.NullInt64
    err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(unread_count), 0) FROM chats`).Scan(&n)
    if err != nil { return 0, fmt.Errorf("chats total_unread: %w", err) }
    return int(n.Int64), nil
}
```

`internal/store/sqlite/messages.go`:
```go
func (s *MessageStore) Count(ctx context.Context) (int, error) {
    var n int
    err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE deleted_at IS NULL`).Scan(&n)
    if err != nil { return 0, fmt.Errorf("messages count: %w", err) }
    return n, nil
}
```

`internal/store/sqlite/contacts.go`:
```go
func (s *ContactStore) Search(ctx context.Context, query string, limit int) ([]store.Contact, error) {
    pat := "%" + strings.ToLower(query) + "%"
    rows, err := s.db.QueryContext(ctx, `
        SELECT `+contactColumns+` FROM contacts
        WHERE lower(coalesce(push_name,'')) LIKE ?
           OR lower(coalesce(full_name,'')) LIKE ?
           OR lower(coalesce(business_name,'')) LIKE ?
        ORDER BY jid ASC
        LIMIT ?
    `, pat, pat, pat, limit)
    // ... scan loop ...
}

func (s *ContactStore) Count(ctx context.Context) (int, error) { ... } // same shape as ChatStore.Count
```

No new migrations. The Plan 03 `0001_init.sql` schema covers everything.

## 7. Service layer

```go
// internal/service/service.go

type Service interface {
    // Plan 02 surface
    Status / LoginQR / LoginPhone / Logout

    // Plan 04 surface
    SendText(ctx, chatJID, text) (store.Message, error)

    // Plan 05 surface
    ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error)
    GetChat(ctx context.Context, jid string) (store.Chat, error)
    ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error)
    SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error)
    ListContacts(ctx context.Context) ([]store.Contact, error)
    SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error)
    Stats(ctx context.Context) (Stats, error)
}

type Stats struct {
    Chats       int `json:"chats"`
    Messages    int `json:"messages"`
    Contacts    int `json:"contacts"`
    UnreadTotal int `json:"unread_total"`
}
```

Limit clamping: every method that takes a `limit` validates `1 <= limit <= 100` and returns `ErrInvalidRequest` (Plan 04 sentinel) on violation. Default values are NOT applied at the service layer — that's the HTTP handler's job (the service expects a clamped value). Rationale: keeps service-layer behavior deterministic and easier to test.

`GetChat` returns `store.ErrNotFound` directly (already wrapped at the SQLite layer); the handler maps it to 404.

Search query validation: `SearchMessages` and `SearchContacts` reject `query == ""` with `ErrInvalidRequest`. No upper bound on query length (whatsmeow's text limit doesn't apply to search).

Service `Stats` runs the four count queries SEQUENTIALLY (no `errgroup`). The DB has microseconds of latency for these counts in the current workload; parallelizing is premature.

## 8. HTTP handlers

Five new files / additions:

**`internal/transport/http/chats.go`** (new):

```go
func ListChatsHandler(svc service.Service) http.Handler {
    // parse query params:
    //   limit (default 50, max 100; bad → 400)
    //   before (RFC 3339; absent or zero → time.Time{}; bad → 400)
    //   include_archived ("true"/"false", default false; bad → 400)
    // call svc.ListChats(ctx, before, limit, includeArchived)
    // return {"chats": [...]} as JSON
}

func GetChatHandler(svc service.Service) http.Handler {
    // path param: jid (chi.URLParam)
    // call svc.GetChat(ctx, jid)
    // store.ErrNotFound → 404 chat.not_found
    // return {jid, name, kind, last_msg_at, unread_count, archived} as JSON
}

func ListMessagesByChatHandler(svc service.Service) http.Handler {
    // path: jid; query: limit, before (same parsing as ListChats minus include_archived)
    // call svc.ListMessages(ctx, jid, before, limit)
    // return {"messages": [...]} as JSON
}
```

**`internal/transport/http/messages.go`** (extend Plan 04):
```go
// existing: SendTextHandler (Plan 04)

func SearchMessagesHandler(svc service.Service) http.Handler {
    // query: q (required), limit (default 50, max 100)
    // call svc.SearchMessages(ctx, q, limit)
    // return {"messages": [...]} as JSON
}
```

**`internal/transport/http/contacts.go`** (new):
```go
func ListContactsHandler(svc service.Service) http.Handler {
    // no query params
    // return {"contacts": [...]}
}

func SearchContactsHandler(svc service.Service) http.Handler {
    // query: q (required), limit (default 50, max 100)
    // return {"contacts": [...]}
}
```

**`internal/transport/http/stats.go`** (new):
```go
func StatsHandler(svc service.Service) http.Handler {
    // call svc.Stats(ctx)
    // return service.Stats encoded directly (it has json tags)
}
```

**`internal/transport/http/router.go`** — append seven routes to the auth-protected group:
```go
r.Method(http.MethodGet, "/chats", ListChatsHandler(d.Service))
r.Method(http.MethodGet, "/chats/{jid}", GetChatHandler(d.Service))
r.Method(http.MethodGet, "/chats/{jid}/messages", ListMessagesByChatHandler(d.Service))
r.Method(http.MethodGet, "/messages/search", SearchMessagesHandler(d.Service))
r.Method(http.MethodGet, "/contacts", ListContactsHandler(d.Service))
r.Method(http.MethodGet, "/contacts/search", SearchContactsHandler(d.Service))
r.Method(http.MethodGet, "/stats", StatsHandler(d.Service))
```

Response envelopes:
- List endpoints wrap arrays under a singular-object key (`{"chats":[...]}` not `[...]`) so future fields (next-cursor, has-more) can be added without breaking clients.
- Single-resource endpoints (`GetChat`) return the resource at the top level.
- `Stats` returns the Stats struct at the top level — its four fields are stable.

Status mapping:
- 200 — success
- 400 `request.invalid` — bad limit (non-int, < 1, > 100), bad before timestamp, missing q
- 404 `chat.not_found` — only on `GET /v1/chats/{jid}`
- 500 `internal` — any other store/service error

JID validation on the `{jid}` path param: none at the handler — chi's router won't match an empty segment, and SQLite's `Get` returns `store.ErrNotFound` for any non-matching JID. We don't pre-parse JIDs on read endpoints; it's fine to surface 404 for malformed JIDs too. `GetChat` returns the row regardless of `archived` status — clients that want to view archived chats follow the link from `/v1/chats?include_archived=true` and are expected to handle them.

## 9. Wiring

`cmd/whatsmeow-api/serve.go` is unchanged. The `Service` interface grows but `service.New` and the constructor wiring are unaffected.

`internal/transport/http/router.go` gets the seven new route entries (already covered in §8).

## 10. Testing strategy

**Store layer** (`internal/store/sqlite/*_test.go`):
- `TestChatList` — REWRITTEN for the new pagination signature. Cases: empty before + small limit returns the newest N; non-zero before excludes equal/older; `includeArchived` toggles correctly.
- `TestChatCount` — count after seeding 0, 1, N rows.
- `TestChatTotalUnread` — sum across rows including zero-unread chats.
- `TestMessageCount` — counts exclude soft-deleted messages.
- `TestContactSearch` — case-insensitive match on push_name, full_name, business_name; substring match; respects limit; empty query at this layer just matches everything (validation happens above).
- `TestContactCount`.

**Service layer** (`internal/service/service_test.go`):
- Extend `inMemoryBundle` helpers to honor the new methods. The fake `chatStore.List` should respect the cursor + limit + archived flags so service tests can exercise pagination.
- One test per Service method: happy path, validation rejections (bad limit, bad before, empty q), `ErrNotFound` for `GetChat`. `Stats` test asserts the four fields.

**HTTP layer** (`internal/transport/http/{chats,contacts,messages,stats}_test.go`):
- Each handler: happy path (200 + correct JSON shape), 400 (each validation branch), 404 where applicable.
- Use the same fake-Service pattern as Plan 04.

**E2E smoke** (Task in the implementation plan):
- After daemon paired and a few messages exchanged: hit each new endpoint, confirm 200 + non-empty data.
- Bad `limit=foo`, bad `before=2026`, missing `q`: expect 400.
- Unknown JID on `/v1/chats/{jid}`: expect 404.

## 11. File layout

```
internal/store/
  store.go                     extend interfaces
  sqlite/chats.go              List signature change, +Count, +TotalUnread
  sqlite/chats_test.go         rewrite TestChatList; +Count, +TotalUnread
  sqlite/messages.go           +Count
  sqlite/messages_test.go      +TestMessageCount
  sqlite/contacts.go           +Search, +Count
  sqlite/contacts_test.go      +TestContactSearch, +TestContactCount

internal/service/
  service.go                   +Stats type, +7 methods (clamp + delegate)
  service_test.go              extend inMemoryBundle; +tests for the 7 methods

internal/transport/http/
  chats.go (new)               ListChatsHandler, GetChatHandler, ListMessagesByChatHandler
  chats_test.go (new)
  messages.go                  +SearchMessagesHandler
  messages_test.go             +TestSearchMessages*
  contacts.go (new)            ListContactsHandler, SearchContactsHandler
  contacts_test.go (new)
  stats.go (new)               StatsHandler
  stats_test.go (new)
  router.go                    +7 routes

README.md                      status section
```

No files removed. No new dependencies.

## 12. Dependencies

None added. `strings` may already be imported in some files via Plan 03/04.

## 13. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new store / service / HTTP tests.
- Manual smoke: with a paired account that's exchanged at least one message, all seven endpoints return 200 with realistic payloads. Validation paths return 400. Unknown chat JID returns 404.
- Existing Plan 01–04 smoke (`/v1/health`, `/v1/status`, login, logout, `POST /v1/messages`) continues to pass.

## 14. Open questions deferred to implementation

- Whether `SearchMessages` and `SearchContacts` should be combined into a single `/v1/search` endpoint. Current spec keeps them separate to align with the master design's §6.1 endpoint list and to let consumers ask only for what they need.
- Whether `Stats` should include `last_inbound_ts` or `oldest_message_ts` — defer until a consumer asks (YAGNI).
- Whether `/v1/chats?include_archived=true` should sort archived chats separately. Current implementation interleaves them by `last_msg_at`. Revisit if a UX consumer wants segmented output.

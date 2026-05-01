# whatsmeow-api Plan 03 — App Store (SQLite + Migrations) Design

**Date:** 2026-05-01
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 02 (waclient + login) — merged.

## 1. Purpose

Build the chat/message persistence layer the daemon will use from Plan 04 onward. Plan 03 ships the schema, the per-domain `Store` interfaces, a SQLite implementation behind them, and migration plumbing that runs at `serve` startup. No consumers exist yet — Plans 04–09 wire those in.

## 2. Goals

- All seven tables from the master design land at once (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) so future plans don't pay schema-evolution cost for each consumer.
- Per-domain interfaces (interface segregation): handler tests in Plan 04+ inject small focused fakes rather than a single 30-method monolith.
- Migrations run automatically on `serve`, mirroring the whatsmeow session store wiring shipped in Plan 02. Failures abort startup.
- Hand-rolled SQL queries against `database/sql`. The cross-dialect codegen story (`sqlc`) is deferred to Plan 10 when Postgres lands; until then the parity overhead is unjustified.
- Tests at this layer use a real file-backed SQLite (FTS5 triggers, FK enforcement, WAL all exercised). No mocks of the sql driver.

## 3. Non-goals (Plan 03)

- Postgres implementation → Plan 10.
- `sqlc` query codegen → Plan 10.
- Service-layer methods that call into the store (chat list, message list, search, send) → Plan 04+.
- Wiring whatsmeow events to persist incoming messages → Plan 04.
- HTTP endpoints that read or write through these stores → Plan 04+.
- Retention / pruning of `events_log` → Plan 09 (when SSE actually consumes it).
- Migration tooling beyond what's needed for forward progress (no rollback CLI, no `migrate status` subcommand).

## 4. Architecture

```
        ┌─────────────────────────────────────┐
        │  cmd/whatsmeow-api serve            │
        └──────────────────┬──────────────────┘
                           │
                           ▼
        ┌─────────────────────────────────────┐
        │  open data_dir/whatsmeow-app.db     │
        │  → run migrations (golang-migrate)  │
        │  → construct *sqlite.Store          │
        │  → expose store.Bundle into Deps    │
        └──────────────────┬──────────────────┘
                           │
                           ▼
        ┌─────────────────────────────────────┐
        │  store.Bundle{                      │
        │    Chats     ChatStore              │
        │    Messages  MessageStore           │
        │    Contacts  ContactStore           │
        │    Media     MediaStore             │
        │    Events    EventsLog              │
        │    KV        KV                     │
        │  }                                  │
        └──────────────────┬──────────────────┘
                           │
                           ▼
                    httpapi.Deps.Store
                    (unused in Plan 03;
                     consumed in Plan 04+)
```

Plan 03 adds nothing to the request path. The store is built, exposed, and tested in isolation. Validation of the wiring is purely structural ("daemon still boots, migrations apply cleanly, no panic").

## 5. Per-domain interfaces (`internal/store/store.go`)

Each interface lives in `package store` so consumers depend on the abstraction, not the SQLite impl. Method counts are approximate; final shape locks in during Plan 04 once a real consumer surfaces.

- `ChatStore` — `Put`, `Get`, `List`, `SetArchived`
- `MessageStore` — `Put`, `Get`, `ListByChat`, `Search` (FTS), `SoftDelete`
- `ContactStore` — `Put`, `Get`, `List`
- `MediaStore` — `Put`, `GetByMessageID`
- `EventsLog` — `Append`, `SinceSeq`
- `KV` — `Get`, `Set`, `Delete`

```go
type Bundle struct {
    Chats    ChatStore
    Messages MessageStore
    Contacts ContactStore
    Media    MediaStore
    Events   EventsLog
    KV       KV
}
```

Domain types (`Chat`, `Message`, `Contact`, etc.) live alongside the interfaces in `store.go`. They're plain structs with `time.Time` fields where the schema stores Unix-epoch ints, marshalled at the boundary.

## 6. Schema (SQLite)

Single migration, `0001_init`. Field list is final for Plan 03; later plans may add columns via 0002+.

- `chats(jid TEXT PRIMARY KEY, name TEXT, kind TEXT NOT NULL, last_msg_at INT, unread_count INT NOT NULL DEFAULT 0, archived INT NOT NULL DEFAULT 0)`
- `messages(id TEXT PRIMARY KEY, chat_jid TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE, sender_jid TEXT NOT NULL, ts INT NOT NULL, kind TEXT NOT NULL, body TEXT, reply_to TEXT, edited_at INT, deleted_at INT, raw_meta TEXT)`
  - Index `idx_messages_chat_ts ON messages(chat_jid, ts DESC)`
- `messages_fts USING fts5(body, content='messages', content_rowid='rowid')` — synced via three triggers (insert, update, delete on `messages`)
- `contacts(jid TEXT PRIMARY KEY, push_name TEXT, full_name TEXT, business_name TEXT)`
- `media(message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE, mime TEXT NOT NULL, size INT NOT NULL, sha256 TEXT NOT NULL, path TEXT NOT NULL)`
- `events_log(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INT NOT NULL, type TEXT NOT NULL, payload TEXT NOT NULL)`
- `kv(key TEXT PRIMARY KEY, value TEXT NOT NULL)`

`messages.id` is the whatsmeow native message id (a string like `3EB05...`), not a UUID — it's what whatsmeow events deliver and what survives roundtrips. JIDs are stored as the canonical string from `whatsmeow/types.JID.String()` (`27821234567@s.whatsapp.net`, `…@g.us`, etc.).

`PRAGMA foreign_keys = ON` and `PRAGMA journal_mode = WAL` are set on connection open in `sqlite.Store` (the migration file itself doesn't set them; pragmas don't survive across connections).

## 7. Migrations

`github.com/golang-migrate/migrate/v4` with the `iofs` source driver. Files live at `internal/store/migrations/sqlite/0001_init.{up,down}.sql` and are embedded via `//go:embed sqlite/*.sql` in `internal/store/migrations/embed.go`.

`sqlite.Store.New(ctx, path)`:
1. `sql.Open("sqlite", dsn)` with the WAL/FK pragmas in the DSN.
2. Construct an `iofs` source from the embedded FS (subdir `sqlite`).
3. Build a `*migrate.Migrate` with the `sqlite` database driver.
4. Call `Up()`. `migrate.ErrNoChange` is expected on subsequent boots and ignored.
5. Return `*Store` holding the `*sql.DB` and the per-domain sub-store pointers (§8).

Failure at any step bubbles up from `serve.go` → `cobra.Command.RunE` → `os.Exit(1)`.

## 8. SQLite implementation (`internal/store/sqlite/`)

One file per domain, each implementing the matching interface. Hand-rolled SQL using `*sql.DB.QueryRowContext` / `ExecContext` / `QueryContext`. No transactions wrap single-statement methods; multi-statement helpers (e.g. `Put` for `chats` that updates an existing row when present) use `INSERT … ON CONFLICT` instead of an explicit transaction where possible.

`store.go`:
```go
type Store struct {
    db *sql.DB
    Chats    *ChatStore
    Messages *MessageStore
    Contacts *ContactStore
    Media    *MediaStore
    Events   *EventsLog
    KV       *KVStore
}

func New(ctx context.Context, path string) (*Store, error) { ... }
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Bundle() store.Bundle { ... }
```

Each per-domain struct (`ChatStore`, `MessageStore`, etc.) is a pointer-receiver type holding a reference to the same `*sql.DB`. They satisfy the `store.*Store` interfaces by virtue of method signatures alone — no explicit interface declarations needed at the impl level.

## 9. Wiring into serve

`cmd/whatsmeow-api/serve.go` gains:

```go
appDB, err := sqlite.New(ctx, filepath.Join(cfg.DataDir, "whatsmeow-app.db"))
if err != nil { return fmt.Errorf("open app store: %w", err) }
defer appDB.Close()
```

…and passes `appDB.Bundle()` into `httpapi.Deps.Store`.

`internal/transport/http/router.go` gets a `Store store.Bundle` field on `Deps`. No handler reads it in Plan 03; that's Plan 04+.

## 10. Testing strategy

**Per-domain unit tests** (`internal/store/sqlite/<domain>_test.go`):

- Each test file builds a `*sqlite.Store` via `New(ctx, t.TempDir()+"/test.db")` — real file-backed SQLite, real migrations, real FTS5.
- Helpers: `newTestStore(t)` returns `*Store` and registers `t.Cleanup(s.Close)`.
- Coverage per domain:
  - **chats:** Put/Get/List ordering by `last_msg_at`, archived filtering, idempotent Put.
  - **messages:** Put/Get/ListByChat pagination, FTS5 search via `Search`, soft-delete sets `deleted_at` and excludes from list.
  - **contacts:** Put/Get/List basics.
  - **media:** Put/GetByMessageID, FK cascade when parent message is deleted.
  - **events_log:** Append assigns monotonic seq, SinceSeq returns ordered slice.
  - **kv:** Get/Set/Delete; Set is upsert.

**No tests at the interface layer** — interfaces are abstractions, the SQLite impl is the only implementation in Plan 03. Service-level tests in Plan 04+ will inject hand-rolled in-memory fakes implementing the interfaces.

**No daemon-level smoke test.** Plan 03 doesn't add any HTTP behavior; the existing `/v1/health`, `/v1/status`, `/v1/login/*` smoke from Plan 02 still passes after wiring `Store` into Deps. Verification reduces to: daemon boots, app DB file appears in `data_dir/`, `sqlite_master` shows the expected tables.

## 11. File layout

New / modified:

```
internal/store/
  store.go                              # interfaces + Bundle + domain types
  migrations/
    embed.go                            # //go:embed sqlite/*.sql
    sqlite/0001_init.up.sql
    sqlite/0001_init.down.sql
  sqlite/
    store.go                            # *Store, New, Close, Bundle
    chats.go        chats_test.go
    messages.go     messages_test.go    # incl. FTS search
    contacts.go     contacts_test.go
    media.go        media_test.go
    events_log.go   events_log_test.go
    kv.go           kv_test.go
cmd/whatsmeow-api/serve.go              # open app DB, run migrations, build Bundle, pass into Deps
internal/transport/http/router.go       # Deps.Store added (unused in this plan)
go.mod / go.sum                         # +golang-migrate v4 + iofs
README.md                               # status section update
```

Files removed: `internal/store/doc.go`, `internal/store/sqlite/doc.go` (replaced by `store.go` package docs).

## 12. Dependencies (added)

- `github.com/golang-migrate/migrate/v4` — migration runner. Pulls in two subpackages from the same module: `database/sqlite` (driver) and `source/iofs` (embed.FS source).
- `modernc.org/sqlite` — already in go.mod from Plan 02; reused as the underlying `database/sql` driver.

No other runtime deps added.

## 13. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new `internal/store/sqlite/*_test.go`.
- `./bin/whatsmeow-api serve` boots, creates `data_dir/whatsmeow-app.db`, exits cleanly on SIGTERM. `sqlite3 data_dir/whatsmeow-app.db ".tables"` shows the seven app tables plus `schema_migrations`.
- Existing Plan 02 manual smoke (`/v1/health`, `/v1/status`, CLI `status`/`logout`/`login phone`/`login qr` SSE upgrade) continues to pass.

## 14. Open questions deferred to implementation

- Domain-type field names (`Chat.Kind` enum values vs free string) — settle when Plan 04 surfaces actual usage.
- Pagination contract for `MessageStore.ListByChat` (cursor vs offset) — same: defer to first consumer.
- FTS5 tokenizer (`unicode61` vs `porter`) — start with default `unicode61`; revisit if relevance is poor.
- Whether `events_log` entries carry a request_id field — wait until Plan 09 SSE design.

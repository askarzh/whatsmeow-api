# whatsmeow-api Plan 03 — App Store (SQLite + Migrations) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the chat/message persistence layer — all 7 tables from the master design behind per-domain Go interfaces, a SQLite implementation, and `golang-migrate`-driven schema migrations that auto-run on `serve` startup. No consumers wire into the request path; that's Plan 04+.

**Architecture:** A new `package store` defines six per-domain interfaces (`ChatStore`, `MessageStore`, `ContactStore`, `MediaStore`, `EventsLog`, `KV`) plus a `Bundle` aggregator. A new `internal/store/sqlite` package implements them against `database/sql` using `modernc.org/sqlite`. Migrations are SQL files embedded via `//go:embed` and run via `golang-migrate/v4` with the `iofs` source driver. `serve.go` constructs the store at startup, runs migrations, and exposes the `Bundle` through `httpapi.Deps.Store` for future consumption.

**Tech Stack:**
- Go 1.26
- `database/sql` + `modernc.org/sqlite` (already in go.mod from Plan 02)
- `github.com/golang-migrate/migrate/v4` — migration runner; sub-imports `database/sqlite` driver and `source/iofs` source
- All Plan 01/02 stack (chi, cobra, koanf, slog, testify)

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/store.go` | Per-domain interfaces (`ChatStore`, `MessageStore`, `ContactStore`, `MediaStore`, `EventsLog`, `KV`), `Bundle` struct, domain types (`Chat`, `Message`, `Contact`, `MediaRef`, `EventLogEntry`). |
| `internal/store/migrations/embed.go` | `//go:embed sqlite/*.sql` declaration + accessor returning the FS rooted at `sqlite/`. |
| `internal/store/migrations/sqlite/0001_init.up.sql` | Schema for all 7 tables + `idx_messages_chat_ts` index + 3 FTS5 sync triggers. |
| `internal/store/migrations/sqlite/0001_init.down.sql` | Drops everything `0001_init.up.sql` creates. |
| `internal/store/sqlite/store.go` | `*Store`, `New(ctx, path)` (opens DB with WAL/FK pragmas, runs migrations, builds sub-stores), `Close()`, `Bundle()`. |
| `internal/store/sqlite/store_test.go` | Constructor + migration verification: tables exist, idempotent re-open. |
| `internal/store/sqlite/chats.go` | `*ChatStore` implementing `store.ChatStore`. |
| `internal/store/sqlite/chats_test.go` | Put/Get/List/SetArchived. |
| `internal/store/sqlite/messages.go` | `*MessageStore` implementing `store.MessageStore`. |
| `internal/store/sqlite/messages_test.go` | Put/Get/ListByChat/Search (FTS)/SoftDelete. |
| `internal/store/sqlite/contacts.go` | `*ContactStore` implementing `store.ContactStore`. |
| `internal/store/sqlite/contacts_test.go` | Put/Get/List. |
| `internal/store/sqlite/media.go` | `*MediaStore` implementing `store.MediaStore`. |
| `internal/store/sqlite/media_test.go` | Put/GetByMessageID + FK cascade. |
| `internal/store/sqlite/events_log.go` | `*EventsLog` implementing `store.EventsLog`. |
| `internal/store/sqlite/events_log_test.go` | Append/SinceSeq monotonic ordering. |
| `internal/store/sqlite/kv.go` | `*KVStore` implementing `store.KV`. |
| `internal/store/sqlite/kv_test.go` | Get/Set (upsert) /Delete. |
| `internal/transport/http/router.go` | Modified — adds `Store store.Bundle` to `Deps`. |
| `cmd/whatsmeow-api/serve.go` | Modified — opens app DB, runs migrations, passes `Bundle` into Deps, defers `Close`. |
| `README.md` | Modified — status section + table list. |

Files removed: `internal/store/doc.go`, `internal/store/sqlite/doc.go`, `internal/store/postgres/doc.go` (the Postgres dir stays empty until Plan 10; we delete the placeholder doc.go to avoid confusion). Replaced by package docs in `store.go` files.

---

## Task 1: Add Plan 03 dependencies

**Files:** `go.mod`, `go.sum`

- [ ] **Step 1: Add the runtime dependencies**

```bash
cd /home/askar/src/whatsmeow-api
go get github.com/golang-migrate/migrate/v4@latest
```

Note: `database/sqlite` and `source/iofs` are sub-packages of the same module; a single `go get` covers all three.

- [ ] **Step 2: Tidy and verify**

```bash
go mod tidy
go build ./...
go vet ./...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add golang-migrate/v4 for app store migrations"
```

---

## Task 2: store interfaces, Bundle, and domain types

**Files:**
- Create: `internal/store/store.go` (replace `internal/store/doc.go`)

This task ships interfaces only — no implementation, no tests. The SQLite impl tests in Tasks 5-10 are what exercise the contracts.

- [ ] **Step 1: Remove the stub**

```bash
git rm internal/store/doc.go
git rm internal/store/postgres/doc.go
```

(The `internal/store/postgres/` directory stays — Plan 10 will fill it. We just remove the placeholder so the empty dir doesn't carry stale documentation.)

- [ ] **Step 2: Write `internal/store/store.go`**

Create the file:
```go
// Package store defines the daemon's app-level persistence interfaces. The
// SQLite implementation lives in internal/store/sqlite; Plan 10 will add a
// Postgres impl in internal/store/postgres.
package store

import (
	"context"
	"time"
)

// Chat is one conversation — direct or group.
type Chat struct {
	JID         string
	Name        string
	Kind        string // "user" | "group" | "broadcast"
	LastMsgAt   time.Time
	UnreadCount int
	Archived    bool
}

// Message is one persisted message in a chat.
type Message struct {
	ID         string // whatsmeow's native id (e.g. "3EB05ABC...")
	ChatJID    string
	SenderJID  string
	Timestamp  time.Time
	Kind       string // "text" | "image" | "video" | "audio" | "document" | "sticker" | "system"
	Body       string
	ReplyTo    string // empty if not a reply
	EditedAt   *time.Time
	DeletedAt  *time.Time
	RawMeta    string // JSON-encoded passthrough of the whatsmeow event
}

// Contact is a known WhatsApp identity.
type Contact struct {
	JID          string
	PushName     string
	FullName     string
	BusinessName string
}

// MediaRef points to an on-disk attachment for a message.
type MediaRef struct {
	MessageID string
	MIME      string
	Size      int64
	SHA256    string
	Path      string
}

// EventLogEntry is one row in events_log, used by SSE Last-Event-ID resume.
type EventLogEntry struct {
	Seq     int64
	Time    time.Time
	Type    string
	Payload string // JSON-encoded
}

// ChatStore manages the chats table.
type ChatStore interface {
	Put(ctx context.Context, c Chat) error
	Get(ctx context.Context, jid string) (Chat, error)
	List(ctx context.Context, includeArchived bool) ([]Chat, error)
	SetArchived(ctx context.Context, jid string, archived bool) error
}

// MessageStore manages the messages table and FTS index.
type MessageStore interface {
	Put(ctx context.Context, m Message) error
	Get(ctx context.Context, id string) (Message, error)
	ListByChat(ctx context.Context, chatJID string, limit int, beforeTS time.Time) ([]Message, error)
	Search(ctx context.Context, query string, limit int) ([]Message, error)
	SoftDelete(ctx context.Context, id string, when time.Time) error
}

// ContactStore manages the contacts table.
type ContactStore interface {
	Put(ctx context.Context, c Contact) error
	Get(ctx context.Context, jid string) (Contact, error)
	List(ctx context.Context) ([]Contact, error)
}

// MediaStore manages the media table.
type MediaStore interface {
	Put(ctx context.Context, m MediaRef) error
	GetByMessageID(ctx context.Context, messageID string) (MediaRef, error)
}

// EventsLog manages the bounded events_log used for SSE resume.
type EventsLog interface {
	Append(ctx context.Context, entry EventLogEntry) (int64, error)
	SinceSeq(ctx context.Context, seq int64, limit int) ([]EventLogEntry, error)
}

// KV is small daemon state.
type KV interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// Bundle aggregates the per-domain interfaces. Constructed by the SQLite store
// (or, in Plan 10, the Postgres store) and passed into HTTP handlers via Deps.
type Bundle struct {
	Chats    ChatStore
	Messages MessageStore
	Contacts ContactStore
	Media    MediaStore
	Events   EventsLog
	KV       KV
}

// ErrNotFound is returned by Get* methods when the key is absent.
var ErrNotFound = sentinelError("store: not found")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }
```

- [ ] **Step 3: Build**

```bash
go build ./...
go vet ./...
```

Expected: clean. No tests yet at this layer.

- [ ] **Step 4: Commit**

```bash
git add internal/store/store.go
git rm internal/store/doc.go internal/store/postgres/doc.go 2>/dev/null
git commit -m "store: per-domain interfaces and Bundle aggregator"
```

(The `git rm` may already be staged from Step 1. `git commit` should still pick them up.)

---

## Task 3: Migration files + embed.go

**Files:**
- Create: `internal/store/migrations/embed.go`
- Create: `internal/store/migrations/sqlite/0001_init.up.sql`
- Create: `internal/store/migrations/sqlite/0001_init.down.sql`

- [ ] **Step 1: Create the up migration**

Create `internal/store/migrations/sqlite/0001_init.up.sql`:
```sql
CREATE TABLE chats (
    jid          TEXT PRIMARY KEY,
    name         TEXT,
    kind         TEXT NOT NULL,
    last_msg_at  INTEGER,
    unread_count INTEGER NOT NULL DEFAULT 0,
    archived     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    chat_jid    TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE,
    sender_jid  TEXT NOT NULL,
    ts          INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    body        TEXT,
    reply_to    TEXT,
    edited_at   INTEGER,
    deleted_at  INTEGER,
    raw_meta    TEXT
);

CREATE INDEX idx_messages_chat_ts ON messages(chat_jid, ts DESC);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    body,
    content='messages',
    content_rowid='rowid'
);

CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
END;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TABLE contacts (
    jid           TEXT PRIMARY KEY,
    push_name     TEXT,
    full_name     TEXT,
    business_name TEXT
);

CREATE TABLE media (
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    path       TEXT NOT NULL
);

CREATE TABLE events_log (
    seq     INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      INTEGER NOT NULL,
    type    TEXT NOT NULL,
    payload TEXT NOT NULL
);

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

- [ ] **Step 2: Create the down migration**

Create `internal/store/migrations/sqlite/0001_init.down.sql`:
```sql
DROP TABLE IF EXISTS kv;
DROP TABLE IF EXISTS events_log;
DROP TABLE IF EXISTS media;
DROP TABLE IF EXISTS contacts;
DROP TRIGGER IF EXISTS messages_au;
DROP TRIGGER IF EXISTS messages_ad;
DROP TRIGGER IF EXISTS messages_ai;
DROP TABLE IF EXISTS messages_fts;
DROP INDEX IF EXISTS idx_messages_chat_ts;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chats;
```

- [ ] **Step 3: Create the embed wrapper**

Create `internal/store/migrations/embed.go`:
```go
// Package migrations embeds the SQL migration files for the app store.
// Plan 10 will add a `postgres/` sibling.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql
var sqliteFiles embed.FS

// SQLite returns the embedded migration files rooted so that file names look
// like "0001_init.up.sql" (rather than "sqlite/0001_init.up.sql").
// golang-migrate's iofs source expects this layout.
func SQLite() fs.FS {
	sub, err := fs.Sub(sqliteFiles, "sqlite")
	if err != nil {
		// Compile-time impossibility: the //go:embed directive is fixed.
		panic(err)
	}
	return sub
}
```

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: clean. No code uses the embed yet — that's Task 4.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/
git commit -m "store: 0001_init migration + embed wrapper"
```

---

## Task 4: SQLite store constructor + migration runner

**Files:**
- Create: `internal/store/sqlite/store.go` (replace `internal/store/sqlite/doc.go`)
- Test: `internal/store/sqlite/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/store_test.go`:
```go
package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCreatesAllTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Open a second handle to inspect sqlite_master without going through s.
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	expectedTables := []string{
		"chats", "contacts", "events_log", "kv", "media", "messages", "messages_fts",
	}
	for _, table := range expectedTables {
		var name string
		err := raw.QueryRowContext(
			context.Background(),
			`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?`,
			table,
		).Scan(&name)
		assert.NoError(t, err, "table %q should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestNewIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s1, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	// Re-opening the same DB should succeed (migrations are no-op on second run).
	s2, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}

func TestBundleFieldsNonNil(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	b := s.Bundle()
	assert.NotNil(t, b.Chats)
	assert.NotNil(t, b.Messages)
	assert.NotNil(t, b.Contacts)
	assert.NotNil(t, b.Media)
	assert.NotNil(t, b.Events)
	assert.NotNil(t, b.KV)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/...
```

Expected: FAIL — `sqlite.New` undefined.

- [ ] **Step 3: Remove the stub**

```bash
git rm internal/store/sqlite/doc.go
```

- [ ] **Step 4: Write `internal/store/sqlite/store.go`**

```go
// Package sqlite implements the app-store interfaces from internal/store
// against a SQLite database file. Drivers are registered via blank imports
// in the store constructor.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/migrations"

	"github.com/golang-migrate/migrate/v4"
	migsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// Store is the SQLite implementation of the app store. It holds the underlying
// *sql.DB plus per-domain sub-stores; Bundle() exposes them as the interface
// types defined in package store.
type Store struct {
	db *sql.DB

	chats    *ChatStore
	messages *MessageStore
	contacts *ContactStore
	media    *MediaStore
	events   *EventsLog
	kv       *KVStore
}

// New opens (or creates) the SQLite database at path, runs all pending
// migrations, and returns a *Store ready for use.
func New(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{db: db}
	s.chats = &ChatStore{db: db}
	s.messages = &MessageStore{db: db}
	s.contacts = &ContactStore{db: db}
	s.media = &MediaStore{db: db}
	s.events = &EventsLog{db: db}
	s.kv = &KVStore{db: db}
	return s, nil
}

// Close releases the underlying *sql.DB.
func (s *Store) Close() error {
	return s.db.Close()
}

// Bundle returns the store interfaces for use by HTTP handlers.
func (s *Store) Bundle() store.Bundle {
	return store.Bundle{
		Chats:    s.chats,
		Messages: s.messages,
		Contacts: s.contacts,
		Media:    s.media,
		Events:   s.events,
		KV:       s.kv,
	}
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrations.SQLite(), ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	driver, err := migsqlite.WithInstance(db, &migsqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrate sqlite driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("migrate.NewWithInstance: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
```

> Note for the implementer: the migrate sqlite driver may be `migsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"` or `migsqlite3 ".../sqlite3"` depending on which dialect token golang-migrate expects with the modernc driver. If the build complains about the driver name, run `go doc github.com/golang-migrate/migrate/v4/database/sqlite` to confirm; both drivers exist and the right one matches whatever string `sql.Open(driverName, ...)` was registered with. Our driver is `modernc.org/sqlite` registered as `"sqlite"`, so the matching golang-migrate package is `database/sqlite`.

- [ ] **Step 5: Add stub per-domain types so the package compiles**

Tasks 5-10 will fill these in. For now create empty placeholders so `Store{}` literal in `New` compiles.

Create `internal/store/sqlite/chats.go`:
```go
package sqlite

import "database/sql"

type ChatStore struct{ db *sql.DB }
```

Create `internal/store/sqlite/messages.go`:
```go
package sqlite

import "database/sql"

type MessageStore struct{ db *sql.DB }
```

Create `internal/store/sqlite/contacts.go`:
```go
package sqlite

import "database/sql"

type ContactStore struct{ db *sql.DB }
```

Create `internal/store/sqlite/media.go`:
```go
package sqlite

import "database/sql"

type MediaStore struct{ db *sql.DB }
```

Create `internal/store/sqlite/events_log.go`:
```go
package sqlite

import "database/sql"

type EventsLog struct{ db *sql.DB }
```

Create `internal/store/sqlite/kv.go`:
```go
package sqlite

import "database/sql"

type KVStore struct{ db *sql.DB }
```

These stubs are intentional — Task 4 only verifies the migration plumbing works. Tasks 5-10 each TDD-grow one of these files.

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/store/sqlite/...
```

Expected: PASS — three tests green.

- [ ] **Step 7: Commit**

```bash
git add internal/store/sqlite/ go.mod go.sum
git rm internal/store/sqlite/doc.go 2>/dev/null
git commit -m "store/sqlite: store constructor + migration runner"
```

---

## Task 5: ChatStore SQLite implementation

**Files:**
- Modify: `internal/store/sqlite/chats.go`
- Test: `internal/store/sqlite/chats_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/chats_test.go`:
```go
package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore opens a fresh SQLite store in a temp dir.
func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestChatPutGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chats := s.Bundle().Chats

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	c := store.Chat{
		JID:         "27821234567@s.whatsapp.net",
		Name:        "Alice",
		Kind:        "user",
		LastMsgAt:   now,
		UnreadCount: 3,
		Archived:    false,
	}
	require.NoError(t, chats.Put(ctx, c))

	got, err := chats.Get(ctx, c.JID)
	require.NoError(t, err)
	assert.Equal(t, c.JID, got.JID)
	assert.Equal(t, c.Name, got.Name)
	assert.Equal(t, c.Kind, got.Kind)
	assert.Equal(t, c.UnreadCount, got.UnreadCount)
	assert.False(t, got.Archived)
	assert.True(t, got.LastMsgAt.Equal(now), "last_msg_at roundtrip")
}

func TestChatGetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Bundle().Chats.Get(context.Background(), "nope@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestChatPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats
	jid := "27821234567@s.whatsapp.net"

	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "old", Kind: "user"}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "new", Kind: "user"}))

	got, err := chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func TestChatList(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats

	t1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	require.NoError(t, chats.Put(ctx, store.Chat{JID: "a@s.whatsapp.net", Name: "A", Kind: "user", LastMsgAt: t1}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "b@s.whatsapp.net", Name: "B", Kind: "user", LastMsgAt: t3}))
	require.NoError(t, chats.Put(ctx, store.Chat{JID: "c@s.whatsapp.net", Name: "C", Kind: "user", LastMsgAt: t2, Archived: true}))

	// Default: archived excluded, ordered by last_msg_at DESC.
	got, err := chats.List(ctx, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[1].JID)

	// includeArchived: returns all, still ordered by last_msg_at DESC.
	got, err = chats.List(ctx, true)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "c@s.whatsapp.net", got[1].JID)
	assert.Equal(t, "a@s.whatsapp.net", got[2].JID)
}

func TestChatSetArchived(t *testing.T) {
	ctx := context.Background()
	chats := newTestStore(t).Bundle().Chats
	jid := "x@s.whatsapp.net"
	require.NoError(t, chats.Put(ctx, store.Chat{JID: jid, Name: "X", Kind: "user"}))

	require.NoError(t, chats.SetArchived(ctx, jid, true))
	got, err := chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.True(t, got.Archived)

	require.NoError(t, chats.SetArchived(ctx, jid, false))
	got, err = chats.Get(ctx, jid)
	require.NoError(t, err)
	assert.False(t, got.Archived)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestChat
```

Expected: FAIL — `chats.Put` etc. not defined (they're methods on `*ChatStore`, but the type currently has none).

- [ ] **Step 3: Implement `internal/store/sqlite/chats.go`**

Replace the stub:
```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ChatStore struct{ db *sql.DB }

const (
	chatColumns = `jid, name, kind, last_msg_at, unread_count, archived`
)

func (s *ChatStore) Put(ctx context.Context, c store.Chat) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (jid, name, kind, last_msg_at, unread_count, archived)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			last_msg_at = excluded.last_msg_at,
			unread_count = excluded.unread_count,
			archived = excluded.archived
	`,
		c.JID, c.Name, c.Kind, unixOrNil(c.LastMsgAt), c.UnreadCount, boolToInt(c.Archived),
	)
	if err != nil {
		return fmt.Errorf("chats put: %w", err)
	}
	return nil
}

func (s *ChatStore) Get(ctx context.Context, jid string) (store.Chat, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE jid = ?`, jid)
	c, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Chat{}, store.ErrNotFound
	}
	if err != nil {
		return store.Chat{}, fmt.Errorf("chats get: %w", err)
	}
	return c, nil
}

func (s *ChatStore) List(ctx context.Context, includeArchived bool) ([]store.Chat, error) {
	q := `SELECT ` + chatColumns + ` FROM chats`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	q += ` ORDER BY last_msg_at DESC NULLS LAST, jid ASC`
	rows, err := s.db.QueryContext(ctx, q)
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

func (s *ChatStore) SetArchived(ctx context.Context, jid string, archived bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET archived = ? WHERE jid = ?`, boolToInt(archived), jid)
	if err != nil {
		return fmt.Errorf("chats set_archived: %w", err)
	}
	return nil
}

// scanner matches both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanChat(s scanner) (store.Chat, error) {
	var (
		c           store.Chat
		name        sql.NullString
		lastMsgAt   sql.NullInt64
		archivedInt int
	)
	if err := s.Scan(&c.JID, &name, &c.Kind, &lastMsgAt, &c.UnreadCount, &archivedInt); err != nil {
		return store.Chat{}, err
	}
	c.Name = name.String
	c.Archived = archivedInt != 0
	if lastMsgAt.Valid {
		c.LastMsgAt = unixToTime(lastMsgAt.Int64)
	}
	return c, nil
}
```

- [ ] **Step 4: Add the shared time helpers**

Create `internal/store/sqlite/time.go`:
```go
package sqlite

import "time"

// unixOrNil returns t.Unix() if t is non-zero, otherwise nil for SQL NULL.
func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

// unixToTime converts a stored unix timestamp into a UTC time.Time.
func unixToTime(sec int64) time.Time {
	return time.Unix(sec, 0).UTC()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestChat -v
```

Expected: all 5 chat tests PASS, plus the 3 store_test tests still pass.

```bash
go test ./internal/store/sqlite/... -v
```

Expected: 8 PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlite/chats.go internal/store/sqlite/chats_test.go internal/store/sqlite/time.go
git commit -m "store/sqlite: ChatStore impl with upsert + archive filter"
```

---

## Task 6: MessageStore SQLite implementation (incl. FTS)

**Files:**
- Modify: `internal/store/sqlite/messages.go`
- Test: `internal/store/sqlite/messages_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/messages_test.go`:
```go
package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedChat(t *testing.T, b store.Bundle, jid string) {
	t.Helper()
	require.NoError(t, b.Chats.Put(context.Background(), store.Chat{JID: jid, Name: jid, Kind: "user"}))
}

func TestMessagePutGet(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "27821234567@s.whatsapp.net"
	seedChat(t, b, chat)

	ts := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	m := store.Message{
		ID:        "MSG1",
		ChatJID:   chat,
		SenderJID: chat,
		Timestamp: ts,
		Kind:      "text",
		Body:      "hello world",
		RawMeta:   `{"foo":"bar"}`,
	}
	require.NoError(t, b.Messages.Put(ctx, m))

	got, err := b.Messages.Get(ctx, "MSG1")
	require.NoError(t, err)
	assert.Equal(t, m.ID, got.ID)
	assert.Equal(t, m.ChatJID, got.ChatJID)
	assert.Equal(t, m.Body, got.Body)
	assert.Equal(t, m.RawMeta, got.RawMeta)
	assert.True(t, got.Timestamp.Equal(ts))
	assert.Nil(t, got.EditedAt)
	assert.Nil(t, got.DeletedAt)
}

func TestMessageGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Messages.Get(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMessagePutRequiresExistingChat(t *testing.T) {
	// FK should reject a message whose chat_jid isn't in chats.
	err := newTestStore(t).Bundle().Messages.Put(context.Background(), store.Message{
		ID: "x", ChatJID: "ghost@s.whatsapp.net", SenderJID: "ghost@s.whatsapp.net",
		Timestamp: time.Now(), Kind: "text", Body: "hi",
	})
	assert.Error(t, err)
}

func TestMessageListByChat(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	mk := func(id string, secs int) store.Message {
		return store.Message{
			ID: id, ChatJID: chat, SenderJID: chat,
			Timestamp: time.Unix(int64(secs), 0).UTC(),
			Kind: "text", Body: id,
		}
	}
	for _, m := range []store.Message{mk("a", 100), mk("b", 200), mk("c", 300), mk("d", 400)} {
		require.NoError(t, b.Messages.Put(ctx, m))
	}

	// limit=2, no cursor → newest two.
	got, err := b.Messages.ListByChat(ctx, chat, 2, time.Time{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "d", got[0].ID)
	assert.Equal(t, "c", got[1].ID)

	// limit=10 with beforeTS = 300 → only a, b (older than 300, excluding 300 itself).
	got, err = b.Messages.ListByChat(ctx, chat, 10, time.Unix(300, 0).UTC())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[0].ID)
	assert.Equal(t, "a", got[1].ID)
}

func TestMessageSearchFTS(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	for _, m := range []store.Message{
		{ID: "1", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "the quick brown fox"},
		{ID: "2", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "lazy dog jumps"},
		{ID: "3", ChatJID: chat, SenderJID: chat, Timestamp: time.Now(), Kind: "text", Body: "FOX hunts mice"},
	} {
		require.NoError(t, b.Messages.Put(ctx, m))
	}

	got, err := b.Messages.Search(ctx, "fox", 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids := []string{got[0].ID, got[1].ID}
	assert.Contains(t, ids, "1")
	assert.Contains(t, ids, "3")
}

func TestMessageSoftDelete(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	chat := "c@s.whatsapp.net"
	seedChat(t, b, chat)

	m := store.Message{
		ID: "x", ChatJID: chat, SenderJID: chat,
		Timestamp: time.Unix(100, 0).UTC(), Kind: "text", Body: "secret",
	}
	require.NoError(t, b.Messages.Put(ctx, m))

	when := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, b.Messages.SoftDelete(ctx, "x", when))

	got, err := b.Messages.Get(ctx, "x")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
	assert.True(t, got.DeletedAt.Equal(when))

	// ListByChat excludes soft-deleted rows.
	list, err := b.Messages.ListByChat(ctx, chat, 10, time.Time{})
	require.NoError(t, err)
	assert.Empty(t, list)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestMessage
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement `internal/store/sqlite/messages.go`**

Replace the stub:
```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MessageStore struct{ db *sql.DB }

const messageColumns = `id, chat_jid, sender_jid, ts, kind, body, reply_to, edited_at, deleted_at, raw_meta`

func (s *MessageStore) Put(ctx context.Context, m store.Message) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, chat_jid, sender_jid, ts, kind, body, reply_to, edited_at, deleted_at, raw_meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			chat_jid = excluded.chat_jid,
			sender_jid = excluded.sender_jid,
			ts = excluded.ts,
			kind = excluded.kind,
			body = excluded.body,
			reply_to = excluded.reply_to,
			edited_at = excluded.edited_at,
			deleted_at = excluded.deleted_at,
			raw_meta = excluded.raw_meta
	`,
		m.ID, m.ChatJID, m.SenderJID, m.Timestamp.Unix(), m.Kind,
		nullableString(m.Body), nullableString(m.ReplyTo),
		ptrUnix(m.EditedAt), ptrUnix(m.DeletedAt),
		nullableString(m.RawMeta),
	)
	if err != nil {
		return fmt.Errorf("messages put: %w", err)
	}
	return nil
}

func (s *MessageStore) Get(ctx context.Context, id string) (store.Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Message{}, store.ErrNotFound
	}
	if err != nil {
		return store.Message{}, fmt.Errorf("messages get: %w", err)
	}
	return m, nil
}

func (s *MessageStore) ListByChat(ctx context.Context, chatJID string, limit int, beforeTS time.Time) ([]store.Message, error) {
	q := `SELECT ` + messageColumns + ` FROM messages WHERE chat_jid = ? AND deleted_at IS NULL`
	args := []any{chatJID}
	if !beforeTS.IsZero() {
		q += ` AND ts < ?`
		args = append(args, beforeTS.Unix())
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("messages list_by_chat: %w", err)
	}
	defer rows.Close()
	var out []store.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("messages list_by_chat scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MessageStore) Search(ctx context.Context, query string, limit int) ([]store.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixCols(messageColumns, "m.")+`
		FROM messages_fts f
		JOIN messages m ON m.rowid = f.rowid
		WHERE messages_fts MATCH ? AND m.deleted_at IS NULL
		ORDER BY m.ts DESC
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("messages search: %w", err)
	}
	defer rows.Close()
	var out []store.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("messages search scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MessageStore) SoftDelete(ctx context.Context, id string, when time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET deleted_at = ? WHERE id = ?`, when.Unix(), id)
	if err != nil {
		return fmt.Errorf("messages soft_delete: %w", err)
	}
	return nil
}

func scanMessage(s scanner) (store.Message, error) {
	var (
		m         store.Message
		body      sql.NullString
		replyTo   sql.NullString
		editedAt  sql.NullInt64
		deletedAt sql.NullInt64
		rawMeta   sql.NullString
		ts        int64
	)
	if err := s.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &ts, &m.Kind, &body, &replyTo, &editedAt, &deletedAt, &rawMeta); err != nil {
		return store.Message{}, err
	}
	m.Timestamp = unixToTime(ts)
	m.Body = body.String
	m.ReplyTo = replyTo.String
	m.RawMeta = rawMeta.String
	if editedAt.Valid {
		t := unixToTime(editedAt.Int64)
		m.EditedAt = &t
	}
	if deletedAt.Valid {
		t := unixToTime(deletedAt.Int64)
		m.DeletedAt = &t
	}
	return m, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ptrUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// prefixCols rewrites a column list like "a, b, c" into "p.a, p.b, p.c".
func prefixCols(cols, prefix string) string {
	out := ""
	for i, r := range cols {
		switch {
		case i == 0 || cols[i-1] == ' ':
			out += prefix + string(r)
		default:
			out += string(r)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestMessage -v
```

Expected: all 6 message tests PASS.

```bash
go test ./internal/store/sqlite/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/messages.go internal/store/sqlite/messages_test.go
git commit -m "store/sqlite: MessageStore impl with FTS5 search"
```

---

## Task 7: ContactStore SQLite implementation

**Files:**
- Modify: `internal/store/sqlite/contacts.go`
- Test: `internal/store/sqlite/contacts_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/contacts_test.go`:
```go
package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactPutGet(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	c := store.Contact{JID: "1@s.whatsapp.net", PushName: "Alice", FullName: "Alice A.", BusinessName: "ACME"}
	require.NoError(t, cs.Put(ctx, c))

	got, err := cs.Get(ctx, c.JID)
	require.NoError(t, err)
	assert.Equal(t, c, got)
}

func TestContactGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Contacts.Get(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestContactList(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "b@s.whatsapp.net", PushName: "B"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}))

	got, err := cs.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Ordered by jid ASC.
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "b@s.whatsapp.net", got[1].JID)
}

func TestContactPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	jid := "x@s.whatsapp.net"
	require.NoError(t, cs.Put(ctx, store.Contact{JID: jid, PushName: "old"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: jid, PushName: "new"}))
	got, err := cs.Get(ctx, jid)
	require.NoError(t, err)
	assert.Equal(t, "new", got.PushName)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestContact
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement `internal/store/sqlite/contacts.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ContactStore struct{ db *sql.DB }

const contactColumns = `jid, push_name, full_name, business_name`

func (s *ContactStore) Put(ctx context.Context, c store.Contact) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contacts (jid, push_name, full_name, business_name)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			push_name = excluded.push_name,
			full_name = excluded.full_name,
			business_name = excluded.business_name
	`, c.JID, nullableString(c.PushName), nullableString(c.FullName), nullableString(c.BusinessName))
	if err != nil {
		return fmt.Errorf("contacts put: %w", err)
	}
	return nil
}

func (s *ContactStore) Get(ctx context.Context, jid string) (store.Contact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE jid = ?`, jid)
	c, err := scanContact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Contact{}, store.ErrNotFound
	}
	if err != nil {
		return store.Contact{}, fmt.Errorf("contacts get: %w", err)
	}
	return c, nil
}

func (s *ContactStore) List(ctx context.Context) ([]store.Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+` FROM contacts ORDER BY jid ASC`)
	if err != nil {
		return nil, fmt.Errorf("contacts list: %w", err)
	}
	defer rows.Close()
	var out []store.Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, fmt.Errorf("contacts list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanContact(s scanner) (store.Contact, error) {
	var (
		c            store.Contact
		pushName     sql.NullString
		fullName     sql.NullString
		businessName sql.NullString
	)
	if err := s.Scan(&c.JID, &pushName, &fullName, &businessName); err != nil {
		return store.Contact{}, err
	}
	c.PushName = pushName.String
	c.FullName = fullName.String
	c.BusinessName = businessName.String
	return c, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestContact -v
```

Expected: all 4 contact tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/contacts.go internal/store/sqlite/contacts_test.go
git commit -m "store/sqlite: ContactStore impl"
```

---

## Task 8: MediaStore SQLite implementation

**Files:**
- Modify: `internal/store/sqlite/media.go`
- Test: `internal/store/sqlite/media_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/media_test.go`:
```go
package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMessage(t *testing.T, b store.Bundle, msgID, chatJID string) {
	t.Helper()
	seedChat(t, b, chatJID)
	require.NoError(t, b.Messages.Put(context.Background(), store.Message{
		ID: msgID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind: "image", Body: "",
	}))
}

func TestMediaPutGet(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessage(t, b, "M1", "c@s.whatsapp.net")

	mr := store.MediaRef{
		MessageID: "M1",
		MIME:      "image/jpeg",
		Size:      4242,
		SHA256:    "abcdef123",
		Path:      "/data/media/M1.jpg",
	}
	require.NoError(t, b.Media.Put(ctx, mr))

	got, err := b.Media.GetByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Equal(t, mr, got)
}

func TestMediaGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Media.GetByMessageID(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMediaCascadesOnMessageDelete(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessage(t, b, "M1", "c@s.whatsapp.net")
	require.NoError(t, b.Media.Put(ctx, store.MediaRef{
		MessageID: "M1", MIME: "image/jpeg", Size: 1, SHA256: "x", Path: "/p",
	}))

	// Hard-delete the parent message via raw SQL — soft-delete is the app's
	// usual path, but the FK cascades on row removal.
	rawDelete(t, b, "M1")

	_, err := b.Media.GetByMessageID(ctx, "M1")
	assert.True(t, errors.Is(err, store.ErrNotFound), "media row should cascade away")
}
```

The cascade test needs to issue a hard `DELETE` on the parent message — the public `MessageStore` only does soft-delete. We open a sibling `sql.DB` against the same file to run the raw query.

Replace the entire `media_test.go` you've drafted so far with this final version:

```go
package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMessage(t *testing.T, b store.Bundle, msgID, chatJID string) {
	t.Helper()
	seedChat(t, b, chatJID)
	require.NoError(t, b.Messages.Put(context.Background(), store.Message{
		ID: msgID, ChatJID: chatJID, SenderJID: chatJID,
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind: "image", Body: "",
	}))
}

func TestMediaPutGet(t *testing.T) {
	ctx := context.Background()
	b := newTestStore(t).Bundle()
	seedMessage(t, b, "M1", "c@s.whatsapp.net")

	mr := store.MediaRef{
		MessageID: "M1",
		MIME:      "image/jpeg",
		Size:      4242,
		SHA256:    "abcdef123",
		Path:      "/data/media/M1.jpg",
	}
	require.NoError(t, b.Media.Put(ctx, mr))

	got, err := b.Media.GetByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Equal(t, mr, got)
}

func TestMediaGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Media.GetByMessageID(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMediaCascadesOnMessageDelete(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	b := s.Bundle()

	seedMessage(t, b, "M1", "c@s.whatsapp.net")
	require.NoError(t, b.Media.Put(ctx, store.MediaRef{
		MessageID: "M1", MIME: "image/jpeg", Size: 1, SHA256: "x", Path: "/p",
	}))

	rawDelete(t, dbPath, "M1")

	_, err = b.Media.GetByMessageID(ctx, "M1")
	assert.True(t, errors.Is(err, store.ErrNotFound), "media row should cascade away")
}

// rawDelete issues a hard DELETE through a sibling sql.DB so we can exercise
// the FK cascade (the public MessageStore only soft-deletes).
func rawDelete(t *testing.T, dbPath string, msgID string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, msgID)
	require.NoError(t, err)
}
```

(The `modernc.org/sqlite` driver is already registered transitively via `internal/store/sqlite`'s blank import, so no extra import is needed in this test file.)

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestMedia
```

Expected: FAIL — `b.Media.Put` etc. undefined.

- [ ] **Step 3: Implement `internal/store/sqlite/media.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MediaStore struct{ db *sql.DB }

const mediaColumns = `message_id, mime, size, sha256, path`

func (s *MediaStore) Put(ctx context.Context, m store.MediaRef) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media (message_id, mime, size, sha256, path)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			mime = excluded.mime,
			size = excluded.size,
			sha256 = excluded.sha256,
			path = excluded.path
	`, m.MessageID, m.MIME, m.Size, m.SHA256, m.Path)
	if err != nil {
		return fmt.Errorf("media put: %w", err)
	}
	return nil
}

func (s *MediaStore) GetByMessageID(ctx context.Context, messageID string) (store.MediaRef, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE message_id = ?`, messageID)
	var m store.MediaRef
	err := row.Scan(&m.MessageID, &m.MIME, &m.Size, &m.SHA256, &m.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return store.MediaRef{}, store.ErrNotFound
	}
	if err != nil {
		return store.MediaRef{}, fmt.Errorf("media get: %w", err)
	}
	return m, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestMedia -v
```

Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/media.go internal/store/sqlite/media_test.go
git commit -m "store/sqlite: MediaStore impl with FK cascade test"
```

---

## Task 9: EventsLog SQLite implementation

**Files:**
- Modify: `internal/store/sqlite/events_log.go`
- Test: `internal/store/sqlite/events_log_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/events_log_test.go`:
```go
package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsAppendMonotonic(t *testing.T) {
	ctx := context.Background()
	ev := newTestStore(t).Bundle().Events
	now := time.Now().UTC()

	s1, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":1}`})
	require.NoError(t, err)
	s2, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":2}`})
	require.NoError(t, err)
	s3, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":3}`})
	require.NoError(t, err)

	assert.Equal(t, int64(1), s1)
	assert.Equal(t, int64(2), s2)
	assert.Equal(t, int64(3), s3)
}

func TestEventsSinceSeq(t *testing.T) {
	ctx := context.Background()
	ev := newTestStore(t).Bundle().Events
	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		_, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "x", Payload: ""})
		require.NoError(t, err)
	}

	got, err := ev.SinceSeq(ctx, 2, 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, int64(3), got[0].Seq)
	assert.Equal(t, int64(5), got[2].Seq)

	// limit applies.
	got, err = ev.SinceSeq(ctx, 0, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].Seq)
	assert.Equal(t, int64(2), got[1].Seq)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestEvents
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement `internal/store/sqlite/events_log.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type EventsLog struct{ db *sql.DB }

func (s *EventsLog) Append(ctx context.Context, e store.EventLogEntry) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events_log (ts, type, payload) VALUES (?, ?, ?)
	`, e.Time.Unix(), e.Type, e.Payload)
	if err != nil {
		return 0, fmt.Errorf("events_log append: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("events_log last_id: %w", err)
	}
	return id, nil
}

func (s *EventsLog) SinceSeq(ctx context.Context, seq int64, limit int) ([]store.EventLogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, ts, type, payload FROM events_log
		WHERE seq > ? ORDER BY seq ASC LIMIT ?
	`, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("events_log since_seq: %w", err)
	}
	defer rows.Close()
	var out []store.EventLogEntry
	for rows.Next() {
		var (
			e  store.EventLogEntry
			ts int64
		)
		if err := rows.Scan(&e.Seq, &ts, &e.Type, &e.Payload); err != nil {
			return nil, fmt.Errorf("events_log since_seq scan: %w", err)
		}
		e.Time = unixToTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestEvents -v
```

Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/events_log.go internal/store/sqlite/events_log_test.go
git commit -m "store/sqlite: EventsLog impl with monotonic seq"
```

---

## Task 10: KV SQLite implementation

**Files:**
- Modify: `internal/store/sqlite/kv.go`
- Test: `internal/store/sqlite/kv_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite/kv_test.go`:
```go
package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKVSetGetDelete(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV

	require.NoError(t, kv.Set(ctx, "k", "v"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)

	require.NoError(t, kv.Delete(ctx, "k"))
	_, err = kv.Get(ctx, "k")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestKVSetIsUpsert(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV
	require.NoError(t, kv.Set(ctx, "k", "old"))
	require.NoError(t, kv.Set(ctx, "k", "new"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func TestKVDeleteAbsentIsNoop(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV
	assert.NoError(t, kv.Delete(ctx, "missing"))
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/... -run TestKV
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement `internal/store/sqlite/kv.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type KVStore struct{ db *sql.DB }

func (s *KVStore) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("kv get: %w", err)
	}
	return v, nil
}

func (s *KVStore) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO kv (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("kv set: %w", err)
	}
	return nil
}

func (s *KVStore) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("kv delete: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/store/sqlite/... -run TestKV -v
```

Expected: 3 PASS.

```bash
go test ./internal/store/sqlite/... -v
```

Expected: all 23 tests PASS (3 store + 5 chat + 6 message + 4 contact + 3 media + 2 events + 3 kv — adjust if counts are slightly off).

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/kv.go internal/store/sqlite/kv_test.go
git commit -m "store/sqlite: KV impl with upsert"
```

---

## Task 11: Wire app store into serve + Deps

**Files:**
- Modify: `internal/transport/http/router.go` (extend `Deps`)
- Modify: `cmd/whatsmeow-api/serve.go` (open app DB, run migrations, pass Bundle into Deps)

- [ ] **Step 1: Extend `Deps` with the Store field**

Edit `internal/transport/http/router.go`. Find `type Deps struct`:
```go
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	Service service.Service
}
```

Replace with:
```go
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	Service service.Service
	Store   store.Bundle
}
```

Add to the import block:
```go
	"github.com/askarzh/whatsmeow-api/internal/store"
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
go vet ./...
```

Expected: clean. No handler reads `Deps.Store` yet.

- [ ] **Step 3: Open the app DB in serve.go**

Edit `cmd/whatsmeow-api/serve.go`. After the existing whatsmeow session-store block (the `switch cfg.Storage.Backend { ... }` and the `wa.Resume` call), add the app store wiring:

Find:
```go
			if err := wa.Resume(ctx); err != nil {
				logger.Warn("session resume failed; awaiting /v1/login/*", "err", err)
			}

			svc := service.New(wa)

			srv := httpapi.NewServer(httpapi.Deps{
				Config:  cfg,
				Logger:  logger,
				Service: svc,
			})
```

Replace with:
```go
			if err := wa.Resume(ctx); err != nil {
				logger.Warn("session resume failed; awaiting /v1/login/*", "err", err)
			}

			appPath := filepath.Join(cfg.DataDir, "whatsmeow-app.db")
			appDB, err := sqlitestore.New(ctx, appPath)
			if err != nil {
				return fmt.Errorf("open app store: %w", err)
			}
			defer func() { _ = appDB.Close() }()
			logger.Info("app store opened", "path", appPath)

			svc := service.New(wa)

			srv := httpapi.NewServer(httpapi.Deps{
				Config:  cfg,
				Logger:  logger,
				Service: svc,
				Store:   appDB.Bundle(),
			})
```

Add to the import block:
```go
	sqlitestore "github.com/askarzh/whatsmeow-api/internal/store/sqlite"
```

- [ ] **Step 4: Build and run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 5: Smoke — daemon boots, app DB appears**

```bash
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
ls -la data/
```

Expected: `data/` contains `whatsmeow-session.db` (Plan 02) AND `whatsmeow-app.db` (Plan 03), both non-empty. Log shows `app store opened path=data/whatsmeow-app.db`.

Verify schema:
```bash
sqlite3 data/whatsmeow-app.db '.tables'
```

Expected output: `chats   contacts   events_log   kv   media   messages   messages_fts   schema_migrations`
(and possibly the FTS5 shadow tables `messages_fts_config`, `messages_fts_data`, `messages_fts_docsize`, `messages_fts_idx`).

Stop the daemon:
```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
```

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/router.go cmd/whatsmeow-api/serve.go
git commit -m "cmd: serve opens app store and exposes Bundle in Deps"
```

---

## Task 12: README update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status section**

Edit `README.md`. Replace the existing Status block:
```markdown
## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.

App-level chat/message storage and messaging endpoints land in Plan 03 / Plan 04.
```

…with:
```markdown
## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.
- **Plan 03 (app store)** shipped: SQLite-backed persistence layer with seven tables (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) and `golang-migrate`-driven schema migrations that auto-run on `serve`. No handlers read it yet; consumers arrive in Plan 04+.

Messaging endpoints (send / receive / list / search) land in Plan 04+.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 03"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` all PASS
- [ ] Daemon boots, `data/whatsmeow-app.db` exists with the seven app tables + `schema_migrations`
- [ ] Existing Plan 02 manual smoke (`/v1/health`, `/v1/status`, CLI `status`/`logout`/`login phone` bad number, `/v1/login/qr` SSE upgrade) still PASS
- [ ] `git log --oneline` shows ~12 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 04 — send text + receive incoming + persist**.

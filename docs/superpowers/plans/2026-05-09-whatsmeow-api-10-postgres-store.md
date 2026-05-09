# whatsmeow-api Plan 10 — Postgres Store + Integration Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Postgres backend that satisfies the existing `store.Bundle` interface, runs the same test suite as SQLite, and is selectable at startup via `[storage] backend = "postgres"`.

**Architecture:** New `internal/store/postgres/` package mirroring `internal/store/sqlite/` query-for-query. New `internal/store/migrations/postgres/` with three Postgres-native migration sets. New `internal/store/storesuite/` package extracted from the existing SQLite test files — both dialects' test files become thin wrappers that call the shared suite, eliminating drift. Tests use testcontainers-go for a real Postgres in Docker; the Postgres test packages skip cleanly when Docker is unavailable. Service and HTTP layers are unchanged; `cmd/whatsmeow-api/serve.go` switches on `cfg.Storage.Backend` to pick the bundle.

**Tech Stack:**
- Go 1.26
- Plans 01–09 stack
- New deps: `github.com/jackc/pgx/v5` (with `pgx/v5/stdlib` for `database/sql` interop), `github.com/testcontainers/testcontainers-go`
- Existing: `golang-migrate/migrate/v4`, `modernc.org/sqlite`

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/store/migrations/postgres/0001_init.{up,down}.sql` | NEW | Postgres dialect of the chats/messages/contacts/media/events_log/kv schema; `tsvector` column + GIN index for messages search |
| `internal/store/migrations/postgres/0002_reactions.{up,down}.sql` | NEW | reactions table |
| `internal/store/migrations/postgres/0003_receipts.{up,down}.sql` | NEW | receipts table |
| `internal/store/migrations/embed.go` | MODIFY | add `//go:embed postgres/*.sql` and `func Postgres() fs.FS` |
| `internal/store/postgres/store.go` | NEW | `New(ctx, dsn) (*Store, error)`; opens `*sql.DB` via `pgx/v5/stdlib`; runs migrations; `Bundle()` |
| `internal/store/postgres/{chats,contacts,events_log,kv,media,messages,reactions,receipts}.go` | NEW | One file per store interface, query-for-query mirror of SQLite |
| `internal/store/postgres/testutil.go` | NEW | `NewTestStore(t)` — boots a `postgres:16-alpine` testcontainer, runs migrations, returns store + cleanup; `t.Skip` when Docker unavailable |
| `internal/store/postgres/{*}_test.go` | NEW | One per store interface, calls into `storesuite` helpers |
| `internal/store/storesuite/{chats,contacts,events_log,kv,media,messages,reactions,receipts}.go` | NEW | Shared test bodies as `Run*(t, store)` functions |
| `internal/store/sqlite/{*}_test.go` | MODIFY | Convert to thin wrappers that call `storesuite.Run*` |
| `cmd/whatsmeow-api/serve.go` | MODIFY | Switch on `cfg.Storage.Backend`; call `sqlite.New(...)` or `postgres.New(...)` |
| `go.mod`, `go.sum` | MODIFY | `go get github.com/jackc/pgx/v5 github.com/testcontainers/testcontainers-go` |
| `config.example.toml` | MODIFY | Commented Postgres example |
| `README.md` | MODIFY | Plan 10 status entry; how to switch backends |

No service-layer or HTTP-layer changes.

---

## Task 1: Postgres migration files + `Postgres()` embed function

**Files:**
- Create: `internal/store/migrations/postgres/0001_init.up.sql`
- Create: `internal/store/migrations/postgres/0001_init.down.sql`
- Create: `internal/store/migrations/postgres/0002_reactions.up.sql`
- Create: `internal/store/migrations/postgres/0002_reactions.down.sql`
- Create: `internal/store/migrations/postgres/0003_receipts.up.sql`
- Create: `internal/store/migrations/postgres/0003_receipts.down.sql`
- Modify: `internal/store/migrations/embed.go`

**Reference for column inventory:** the existing files in `internal/store/migrations/sqlite/` are the source of truth. Re-read them before writing each Postgres mirror to lock down exact column names.

- [ ] **Step 1: Write `0001_init.up.sql`**

```sql
CREATE TABLE chats (
    jid          TEXT PRIMARY KEY,
    name         TEXT,
    kind         TEXT NOT NULL,
    last_msg_at  TIMESTAMPTZ,
    unread_count INTEGER NOT NULL DEFAULT 0,
    archived     BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    chat_jid    TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE,
    sender_jid  TEXT NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    kind        TEXT NOT NULL,
    body        TEXT,
    reply_to    TEXT,
    edited_at   TIMESTAMPTZ,
    deleted_at  TIMESTAMPTZ,
    raw_meta    TEXT,
    body_tsv    tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(body, ''))) STORED
);

CREATE INDEX idx_messages_chat_ts ON messages (chat_jid, ts DESC);
CREATE INDEX idx_messages_body_tsv ON messages USING GIN (body_tsv);

CREATE TABLE contacts (
    jid           TEXT PRIMARY KEY,
    push_name     TEXT,
    full_name     TEXT,
    business_name TEXT
);

CREATE TABLE media (
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       BIGINT NOT NULL,
    sha256     TEXT NOT NULL,
    path       TEXT NOT NULL
);

CREATE TABLE events_log (
    seq     BIGSERIAL PRIMARY KEY,
    ts      TIMESTAMPTZ NOT NULL,
    type    TEXT NOT NULL,
    payload TEXT NOT NULL
);

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

- [ ] **Step 2: Write `0001_init.down.sql`**

```sql
DROP TABLE IF EXISTS kv;
DROP TABLE IF EXISTS events_log;
DROP TABLE IF EXISTS media;
DROP TABLE IF EXISTS contacts;
DROP INDEX IF EXISTS idx_messages_body_tsv;
DROP INDEX IF EXISTS idx_messages_chat_ts;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chats;
```

(Order matters because of foreign keys; drop children before parents.)

- [ ] **Step 3: Write `0002_reactions.up.sql`**

```sql
CREATE TABLE reactions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    sender_jid TEXT NOT NULL,
    emoji      TEXT NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, sender_jid)
);
CREATE INDEX idx_reactions_message ON reactions (message_id);
```

- [ ] **Step 4: Write `0002_reactions.down.sql`**

```sql
DROP INDEX IF EXISTS idx_reactions_message;
DROP TABLE IF EXISTS reactions;
```

- [ ] **Step 5: Write `0003_receipts.up.sql`**

```sql
CREATE TABLE receipts (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reader_jid TEXT NOT NULL,
    type       TEXT NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, reader_jid, type)
);
CREATE INDEX idx_receipts_message ON receipts (message_id);
```

- [ ] **Step 6: Write `0003_receipts.down.sql`**

```sql
DROP INDEX IF EXISTS idx_receipts_message;
DROP TABLE IF EXISTS receipts;
```

- [ ] **Step 7: Update `embed.go`**

Edit `internal/store/migrations/embed.go`:

```go
// Package migrations embeds the SQL migration files for the app store.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql
var sqliteFiles embed.FS

//go:embed postgres/*.sql
var postgresFiles embed.FS

// SQLite returns the embedded SQLite migration files rooted so that file
// names look like "0001_init.up.sql".
func SQLite() fs.FS {
	sub, err := fs.Sub(sqliteFiles, "sqlite")
	if err != nil {
		panic(err)
	}
	return sub
}

// Postgres returns the embedded Postgres migration files rooted so that file
// names look like "0001_init.up.sql".
func Postgres() fs.FS {
	sub, err := fs.Sub(postgresFiles, "postgres")
	if err != nil {
		panic(err)
	}
	return sub
}
```

- [ ] **Step 8: Build verification**

```bash
cd /home/askar/src/whatsmeow-api/.worktrees/plan-10-postgres
go build ./...
```

Expected: clean. The new files are SQL+Go; the migration files don't break compilation. No tests added in this task.

- [ ] **Step 9: Commit**

```bash
git add internal/store/migrations/postgres/ internal/store/migrations/embed.go
git commit -m "store/migrations: Postgres dialect for chats/messages/contacts/media/events_log/kv/reactions/receipts"
```

---

## Task 2: Postgres store skeleton + testcontainer helper + smoke

**Files:**
- Create: `internal/store/postgres/store.go`
- Create: `internal/store/postgres/testutil.go`
- Create: `internal/store/postgres/store_test.go`
- Modify: `go.mod`, `go.sum`

**Goal:** A `New(ctx, dsn) (*Store, error)` that opens a Postgres connection via pgx/stdlib, runs migrations, and returns a `*Store` with a `Bundle()` method that returns nil sub-stores for now (per-domain stores land in Tasks 4-6). One smoke test that boots a testcontainer, calls `New`, asserts the migrations applied, and closes cleanly.

- [ ] **Step 1: Add deps**

```bash
go get github.com/jackc/pgx/v5
go get github.com/jackc/pgx/v5/stdlib
go get github.com/testcontainers/testcontainers-go
go get github.com/testcontainers/testcontainers-go/modules/postgres
go mod tidy
```

Verify the deps land in `go.mod` direct-imports section as we use them.

- [ ] **Step 2: Implement `internal/store/postgres/store.go`**

```go
// Package postgres is the Postgres implementation of the app store. It mirrors
// the SQLite package's shape: New() opens a connection, runs migrations, and
// returns a *Store that exposes a store.Bundle.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migpgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // register pgx driver under "pgx"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/migrations"
)

// Store is the Postgres implementation of the app store.
type Store struct {
	db *sql.DB

	chats     *ChatStore
	messages  *MessageStore
	contacts  *ContactStore
	media     *MediaStore
	events    *EventsLog
	kv        *KVStore
	reactions *ReactionStore
	receipts  *ReceiptStore
}

// New opens a Postgres connection at dsn, runs all pending migrations, and
// returns a *Store. The DSN is the standard libpq URL or keyword-value form
// (pgx accepts both).
func New(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open pgx: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{db: db}
	// Per-domain sub-stores are wired in Tasks 4-6.
	return s, nil
}

// Close releases the underlying *sql.DB.
func (s *Store) Close() error {
	return s.db.Close()
}

// Bundle returns the store interfaces used by the service layer. Until
// Tasks 4-6 wire the sub-stores, callers should not invoke methods on these
// fields — they will be nil.
func (s *Store) Bundle() store.Bundle {
	return store.Bundle{
		Chats:     s.chats,
		Messages:  s.messages,
		Contacts:  s.contacts,
		Media:     s.media,
		Events:    s.events,
		KV:        s.kv,
		Reactions: s.reactions,
		Receipts:  s.receipts,
	}
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrations.Postgres(), ".")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	driver, err := migpgx.WithInstance(db, &migpgx.Config{})
	if err != nil {
		return fmt.Errorf("migrations driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx", driver)
	if err != nil {
		return fmt.Errorf("migrations new: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations up: %w", err)
	}
	return nil
}
```

> Note: the import path for the migrate Postgres driver may be `database/postgres` rather than `database/pgx/v5` depending on the migrate version. Run `go doc github.com/golang-migrate/migrate/v4/database/pgx/v5` first; if that doesn't exist, use `github.com/golang-migrate/migrate/v4/database/postgres` instead and adapt the import alias and `migrate.NewWithInstance(..., "postgres", driver)`.

- [ ] **Step 3: Implement `testutil.go`**

```go
package postgres

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	dockerCheckOnce sync.Once
	dockerOK        bool
)

func dockerAvailable() bool {
	dockerCheckOnce.Do(func() {
		err := exec.Command("docker", "info").Run()
		dockerOK = err == nil
	})
	return dockerOK
}

// NewTestStore boots a postgres:16-alpine container, runs migrations, and
// returns a connected *Store. Skips the calling test if Docker is unavailable.
// The returned cleanup runs t.Cleanup-bound; callers don't need to call it
// explicitly but may do so to free the container early.
func NewTestStore(t *testing.T) *Store {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("postgres tests require docker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := pgcontainer.RunContainer(ctx,
		pgcontainer.WithDatabase("whatsmeow_api_test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.WithImage("postgres:16-alpine"),
		pgcontainer.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(30*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get container DSN: %v", err)
	}

	store, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	return store
}

// resetTables truncates every table in the public schema. Tests use this to
// isolate from each other without paying for a new container per test.
func resetTables(t *testing.T, s *Store) {
	t.Helper()
	const stmt = `
		TRUNCATE TABLE
			receipts, reactions, kv, events_log,
			media, messages, contacts, chats
		RESTART IDENTITY CASCADE
	`
	if _, err := s.db.ExecContext(context.Background(), stmt); err != nil {
		t.Fatalf("reset tables: %v", err)
	}
}
```

> Note: the import path for `pgcontainer` is `github.com/testcontainers/testcontainers-go/modules/postgres`. If `RunContainer` is named differently in the version we get (the API has churned), check `go doc github.com/testcontainers/testcontainers-go/modules/postgres` and adapt — the goal is "boot a Postgres 16 container and get a DSN."

- [ ] **Step 4: Smoke test in `store_test.go`**

```go
package postgres_test

import (
	"context"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/stretchr/testify/require"
)

func TestNewRunsMigrationsAndCloses(t *testing.T) {
	s := postgres.NewTestStore(t)

	// If we got this far, the container booted, migrations ran without error,
	// and the *Store is non-nil. Sanity: ping again via the bundle's underlying
	// DB by calling a no-op query through any sub-store. Until sub-stores are
	// wired (Task 4-6), assert the bundle is non-zero.
	bundle := s.Bundle()
	_ = bundle // sub-store fields are nil until later tasks; that's expected

	// Re-running migrations is a no-op; verify by closing and reopening.
	require.NoError(t, s.Close())
	_ = context.Background()
}
```

> The smoke test is intentionally minimal — it proves the container + migrations + driver wiring without depending on per-domain stores.

- [ ] **Step 5: Run the smoke test**

```bash
go test ./internal/store/postgres/... -race -v
```

Expected: PASS if Docker is available; SKIPPED if not. Either is acceptable for this task — the goal is "the tooling works when Docker is present."

If Docker is available locally, expect ~3-5s for the container to boot.

- [ ] **Step 6: Build full repo**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. The new Postgres package compiles, doesn't break any existing tests, and the new test either runs or skips.

- [ ] **Step 7: Commit**

```bash
git add internal/store/postgres/ go.mod go.sum
git commit -m "store/postgres: skeleton + testcontainers helper + migration smoke"
```

---

## Task 3: Extract `storesuite` package; convert SQLite tests to call it

**Files:**
- Create: `internal/store/storesuite/{chats,contacts,events_log,kv,media,messages,reactions,receipts}.go`
- Modify: `internal/store/sqlite/{chats,contacts,events_log,kv,media,messages,reactions,receipts}_test.go`

**Goal:** Move every existing SQLite test body into a shared `storesuite` package so each test becomes `Run<Behavior>(t, store)` callable from any dialect's test file. After this task, the SQLite tests still pass — they just call into `storesuite` instead of inlining the assertions. Tasks 4-6 will add Postgres test files that call the same `Run*` helpers.

> The existing SQLite tests in `internal/store/sqlite/*_test.go` are black-box (verified by inspection — they only touch `store.Bundle()` interface methods, never `s.db` directly). This makes the extraction mechanical.

This is a **refactor task**: SQLite test names and assertions stay the same; only the location moves.

- [ ] **Step 1: Inspect the existing SQLite test layout**

```bash
ls internal/store/sqlite/*_test.go
wc -l internal/store/sqlite/*_test.go
grep -c "^func Test" internal/store/sqlite/*_test.go
```

This gives the test count per domain. Plan: extract one domain at a time.

- [ ] **Step 2: Pattern — pick chats as the template**

Read `internal/store/sqlite/chats_test.go`. Each test looks like:

```go
func TestChatPutGet(t *testing.T) {
    ctx := context.Background()
    s := newTestStore(t)
    chats := s.Bundle().Chats
    // ... assertions on `chats` and `ctx` ...
}
```

After extraction, the SQLite test file becomes:

```go
func TestChatPutGet(t *testing.T) {
    s := newTestStore(t)
    storesuite.RunChatPutGet(t, s.Bundle().Chats)
}
```

And `internal/store/storesuite/chats.go` contains:

```go
package storesuite

import (
    "context"
    "testing"

    "github.com/askarzh/whatsmeow-api/internal/store"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func RunChatPutGet(t *testing.T, chats store.ChatStore) {
    ctx := context.Background()
    // ... the original assertions on `chats` ...
}
```

- [ ] **Step 3: Extract `chats` tests**

Create `internal/store/storesuite/chats.go`. For each `TestX` in `internal/store/sqlite/chats_test.go`:
1. Copy the function body (everything after `s := newTestStore(t)` and the bundle resolution).
2. Replace the local store variable's references with the parameter (e.g. `chats` becomes `chats store.ChatStore` parameter).
3. Rename the function `TestChatPutGet` → `RunChatPutGet`.

Then update `internal/store/sqlite/chats_test.go`:
- Remove the body of each `TestX`.
- Replace it with `storesuite.RunX(t, s.Bundle().Chats)`.

Add the import `"github.com/askarzh/whatsmeow-api/internal/store/storesuite"` to the SQLite test file.

- [ ] **Step 4: Run SQLite tests for chats**

```bash
go test ./internal/store/sqlite/... -race -run TestChat -v
```

Expected: same number of tests pass as before. If any test relied on captured variables that don't transfer cleanly (closures, helpers), refactor as needed.

- [ ] **Step 5: Repeat for the other domains**

Repeat Steps 3-4 for each of: `contacts`, `events_log`, `kv`, `media`, `messages`, `reactions`, `receipts`.

For tests that use shared helpers (e.g. a `seedMessages` function in `messages_test.go`), move the helper into `storesuite/messages.go` (unexported within the suite package) so all dialect callers see the same fixture.

- [ ] **Step 6: Run the full SQLite test suite**

```bash
go test ./internal/store/sqlite/... -race -v
```

Expected: every test passes — same names, same assertions, just relocated bodies.

- [ ] **Step 7: Run the full repo suite**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. Nothing outside `internal/store/sqlite/` and `internal/store/storesuite/` should change.

- [ ] **Step 8: Commit**

```bash
git add internal/store/storesuite/ internal/store/sqlite/*_test.go
git commit -m "store/storesuite: shared test suite extracted from sqlite tests; sqlite tests now thin wrappers"
```

---

## Task 4: Port `chats`, `contacts`, `kv` (the simple stores)

**Files:**
- Create: `internal/store/postgres/chats.go`
- Create: `internal/store/postgres/contacts.go`
- Create: `internal/store/postgres/kv.go`
- Create: `internal/store/postgres/chats_test.go`
- Create: `internal/store/postgres/contacts_test.go`
- Create: `internal/store/postgres/kv_test.go`
- Modify: `internal/store/postgres/store.go` (wire the three new sub-stores into `Bundle()`)

**Goal:** Port three of the simpler store interfaces (no FTS, no batched inserts). After this task the `Bundle()` returned by `postgres.NewTestStore(t)` exposes working `Chats`, `Contacts`, `KV` while the rest stay nil. Tests for these three pass against Postgres via the shared `storesuite`.

- [ ] **Step 1: Port `chats.go`**

Read `internal/store/sqlite/chats.go`. Port to Postgres by:
1. Copying the file to `internal/store/postgres/chats.go`.
2. Changing the package to `package postgres`.
3. Replacing every `?` placeholder with `$1, $2, ...` (positional, in order of arg).
4. Removing `time.Time` ↔ Unix-seconds conversions (Postgres `TIMESTAMPTZ` binds `time.Time` natively). For SQLite-specific helpers like `nullableTime` or `unixSeconds`, replace with direct `time.Time` binding — passing `time.Time{}` directly works for nullable columns when paired with `sql.NullTime` for scans.
5. Replacing `INTEGER 0/1` boolean treatment with native `bool` binding.
6. Verifying `INSERT ... ON CONFLICT(col) DO UPDATE SET ...` syntax — this clause is already Postgres-shaped in SQLite (SQLite borrowed the syntax). Should port cleanly.

Example: SQLite `chats.Put` writes:
```go
const stmt = `
    INSERT INTO chats (jid, name, kind, last_msg_at, unread_count, archived)
    VALUES (?, ?, ?, ?, ?, ?)
    ON CONFLICT(jid) DO UPDATE SET
        name = excluded.name,
        ...
`
```

Postgres version:
```go
const stmt = `
    INSERT INTO chats (jid, name, kind, last_msg_at, unread_count, archived)
    VALUES ($1, $2, $3, $4, $5, $6)
    ON CONFLICT(jid) DO UPDATE SET
        name = excluded.name,
        ...
`
```

For nullable `time.Time`, the SQLite version probably uses a custom helper. Postgres native equivalent uses `sql.NullTime` for scans and a `*time.Time` (or zero `time.Time` + an `IF` for inserts) for binds. Match whatever idiom is already used in the SQLite file — consistency over personal style.

- [ ] **Step 2: Port `contacts.go`**

Same mechanics. `contacts` has no nullable times, no FK trickery — should be the cleanest port.

- [ ] **Step 3: Port `kv.go`**

Two columns, three queries. Trivial port.

- [ ] **Step 4: Wire the three sub-stores into `Bundle()`**

Edit `internal/store/postgres/store.go`. In `New(...)`, after the migrations run:

```go
s := &Store{db: db}
s.chats = &ChatStore{db: db}
s.contacts = &ContactStore{db: db}
s.kv = &KVStore{db: db}
return s, nil
```

The other fields stay nil until Tasks 5-6.

- [ ] **Step 5: Write Postgres test files**

Create `internal/store/postgres/chats_test.go`:

```go
package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestChatPutGet(t *testing.T) {
	s := postgres.NewTestStore(t)
	storesuite.RunChatPutGet(t, s.Bundle().Chats)
}

// ... one wrapper per RunX in storesuite/chats.go
```

Per-test container reuse: each test gets a fresh container via `NewTestStore`. That's slow if there are many tests — accept it for v1, optimize later if needed (the testcontainers `Reuse: true` pattern or a package-scoped `TestMain`).

> Optimization (optional): if the chats domain has 8+ tests, consider a package-level `TestMain` that boots one container, exposes a shared `*Store`, and uses `resetTables(t, s)` between tests. The trade-off is more boilerplate vs. ~30s of saved test time. Pick whichever the implementer finds cleanest.

Repeat for `contacts_test.go`, `kv_test.go`.

- [ ] **Step 6: Run the Postgres tests**

```bash
go test ./internal/store/postgres/... -race -run 'TestChat|TestContact|TestKV' -v
```

Expected: PASS (or SKIP if no Docker).

- [ ] **Step 7: Run the full repo suite**

```bash
go test ./... -race
```

Expected: PASS. SQLite tests still pass (they call the same `storesuite` helpers).

- [ ] **Step 8: Commit**

```bash
git add internal/store/postgres/{chats,contacts,kv}.go internal/store/postgres/{chats,contacts,kv}_test.go internal/store/postgres/store.go
git commit -m "store/postgres: chats + contacts + kv (port from sqlite, ts via TIMESTAMPTZ)"
```

---

## Task 5: Port `messages` with `tsvector` Search

**Files:**
- Create: `internal/store/postgres/messages.go`
- Create: `internal/store/postgres/messages_test.go`
- Modify: `internal/store/postgres/store.go` (wire `Messages`)

**Goal:** Port the most complex single-table store. The non-trivial diverging concern is FTS: SQLite uses an FTS5 virtual table + triggers; Postgres uses the `body_tsv` generated column we declared in Task 1's migration.

- [ ] **Step 1: Port `messages.go` shape**

Copy `internal/store/sqlite/messages.go` to `internal/store/postgres/messages.go`. Apply the same mechanical changes as Task 4 (placeholders, time, bool).

For the schema-related differences:
- The Postgres table doesn't have `messages_fts`; the equivalent column is `body_tsv` and it's auto-maintained by the `GENERATED ALWAYS AS ... STORED` clause. So writes don't need to re-insert into a sibling table — the FTS5 trigger choreography from SQLite simply doesn't exist in Postgres.
- All write queries (`Put`, `SoftDelete`, `Edit`, etc.) become simpler: just write to `messages` and let the generated column take care of itself.

- [ ] **Step 2: Rewrite `Search`**

The SQLite version probably looks like:
```go
const searchStmt = `
    SELECT m.id, m.chat_jid, m.sender_jid, ...
    FROM messages_fts f
    JOIN messages m ON m.rowid = f.rowid
    WHERE messages_fts MATCH ?
    ORDER BY rank, m.ts DESC
    LIMIT ?
`
```

Postgres version:
```go
const searchStmt = `
    SELECT id, chat_jid, sender_jid, ts, kind, body, reply_to, edited_at, deleted_at, raw_meta
    FROM messages
    WHERE body_tsv @@ plainto_tsquery('simple', $1)
    ORDER BY ts_rank(body_tsv, plainto_tsquery('simple', $1)) DESC, ts DESC
    LIMIT $2
`
```

`plainto_tsquery` accepts arbitrary user input and produces a query that ANDs the terms. `'simple'` config is intentional — no stemming, matching the SQLite FTS5 default behavior.

- [ ] **Step 3: Wire `Messages` into the bundle**

Edit `internal/store/postgres/store.go`:

```go
s.messages = &MessageStore{db: db}
```

- [ ] **Step 4: Write `messages_test.go`**

Create wrappers for every `RunMessage*` helper in `internal/store/storesuite/messages.go`.

For the search test specifically: `RunMessageSearch` must be tolerant of dialect-specific ranking. If the helper currently asserts exact result ordering, factor that into a "matched messages set" assertion (e.g. `assert.ElementsMatch` on IDs, then a soft check that the most recent matching message is first or one of the top results). This change goes into `storesuite/messages.go` so both dialects benefit.

> If the SQLite test asserts exact ordering AND that ordering wouldn't match Postgres (e.g. it expects FTS5's BM25 ranking), this is the moment to relax the assertion. Document the change in the commit message.

- [ ] **Step 5: Run messages tests on both dialects**

```bash
go test ./internal/store/sqlite/... -run TestMessage -v
go test ./internal/store/postgres/... -run TestMessage -v
```

Both must pass.

- [ ] **Step 6: Full suite**

```bash
go test ./... -race
```

- [ ] **Step 7: Commit**

```bash
git add internal/store/postgres/messages.go internal/store/postgres/messages_test.go internal/store/postgres/store.go internal/store/storesuite/messages.go
git commit -m "store/postgres: messages with tsvector Search (relaxed ranking assertions in storesuite)"
```

---

## Task 6: Port `media`, `reactions`, `receipts`, `events_log`

**Files:**
- Create: `internal/store/postgres/{media,reactions,receipts,events_log}.go`
- Create: `internal/store/postgres/{media,reactions,receipts,events_log}_test.go`
- Modify: `internal/store/postgres/store.go` (wire all four)

**Goal:** Finish porting the remaining four stores. None has FTS-style complexity; the only mild concern is `events_log` because of `BIGSERIAL` (RETURNING the assigned seq).

- [ ] **Step 1: Port `media.go`**

Mechanical port. `media.size` is `BIGINT` in Postgres; the SQLite version probably already uses `int64` so this is transparent.

- [ ] **Step 2: Port `reactions.go`**

Mechanical port. `(message_id, sender_jid)` PK matches SQLite.

- [ ] **Step 3: Port `receipts.go`**

Mechanical port. `(message_id, reader_jid, type)` PK matches SQLite.

- [ ] **Step 4: Port `events_log.go`**

The Postgres version's `Append` uses `RETURNING seq` to capture the auto-assigned `BIGSERIAL`:

```go
const stmt = `
    INSERT INTO events_log (ts, type, payload)
    VALUES ($1, $2, $3)
    RETURNING seq
`
err := el.db.QueryRowContext(ctx, stmt, e.Time, e.Type, e.Payload).Scan(&e.Seq)
```

Postgres handles `time.Time` directly; no Unix-seconds conversion.

For `SinceSeq`:
```go
const stmt = `
    SELECT seq, ts, type, payload
    FROM events_log
    WHERE seq > $1
    ORDER BY seq ASC
    LIMIT $2
`
```

- [ ] **Step 5: Wire all four into Bundle**

Edit `internal/store/postgres/store.go`:

```go
s.media = &MediaStore{db: db}
s.events = &EventsLog{db: db}
s.reactions = &ReactionStore{db: db}
s.receipts = &ReceiptStore{db: db}
```

- [ ] **Step 6: Write four test files**

Each is a thin wrapper calling the corresponding `storesuite.Run*`.

- [ ] **Step 7: Run all Postgres tests**

```bash
go test ./internal/store/postgres/... -race -v
```

- [ ] **Step 8: Run full repo suite**

```bash
go test ./... -race
```

- [ ] **Step 9: Commit**

```bash
git add internal/store/postgres/{media,reactions,receipts,events_log}.go internal/store/postgres/{media,reactions,receipts,events_log}_test.go internal/store/postgres/store.go
git commit -m "store/postgres: media + reactions + receipts + events_log"
```

---

## Task 7: Wire dialect switch in `cmd/whatsmeow-api/serve.go`

**Files:**
- Modify: `cmd/whatsmeow-api/serve.go`

- [ ] **Step 1: Inspect the current serve.go**

```bash
grep -n "sqlite.New\|store.Bundle\|Storage" cmd/whatsmeow-api/serve.go
```

Find the line that calls `sqlite.New(...)` and the surrounding context.

- [ ] **Step 2: Add the switch**

Replace the `sqlite.New(...)` line (and assignment) with:

```go
var (
    appStore  interface{ Close() error }
    bundle    store.Bundle
)
switch cfg.Storage.Backend {
case "sqlite":
    s, err := sqlite.New(ctx, filepath.Join(cfg.DataDir, "whatsmeow-app.db"))
    if err != nil {
        return fmt.Errorf("open sqlite: %w", err)
    }
    appStore = s
    bundle = s.Bundle()
case "postgres":
    s, err := postgres.New(ctx, cfg.Storage.PostgresDSN)
    if err != nil {
        return fmt.Errorf("open postgres: %w", err)
    }
    appStore = s
    bundle = s.Bundle()
default:
    // Validation in config rejects this; defensive only.
    return fmt.Errorf("unsupported storage.backend: %q", cfg.Storage.Backend)
}
defer appStore.Close()
logger.Info("app store opened", "backend", cfg.Storage.Backend)
```

> The exact existing variable names and the precise location of the open / close calls may differ. The goal is "switch on backend, get a bundle, close on shutdown." Adapt the surrounding code to match the existing structure rather than imposing this verbatim.

Add the import `"github.com/askarzh/whatsmeow-api/internal/store/postgres"`.

- [ ] **Step 3: Build + smoke**

```bash
go build ./...
./bin/whatsmeow-api serve  # SQLite default; should still work
# Ctrl-C
```

Then with Postgres (requires Docker for a quick container):

```bash
docker run --rm -d --name pg10 -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16-alpine
sleep 3
WMAPI_STORAGE__BACKEND=postgres \
WMAPI_STORAGE__POSTGRES_DSN=postgres://postgres:test@127.0.0.1:5432/postgres?sslmode=disable \
./bin/whatsmeow-api serve &
sleep 2
curl -s http://127.0.0.1:8080/v1/status
# should see the daemon running with a Postgres-backed store
kill %1
docker stop pg10
```

Expected: daemon starts, `app store opened backend=postgres` in the log, `/v1/status` returns the standard JSON.

- [ ] **Step 4: Test suite**

```bash
go test ./... -race
```

Expected: PASS (Postgres tests skip cleanly if Docker unavailable).

- [ ] **Step 5: Commit**

```bash
git add cmd/whatsmeow-api/serve.go
git commit -m "cmd: dialect switch on cfg.Storage.Backend (sqlite|postgres)"
```

---

## Task 8: Docs + final smoke

**Files:**
- Modify: `README.md`
- Modify: `config.example.toml`

- [ ] **Step 1: Update `config.example.toml`**

Add a commented Postgres example below the `[storage]` block:

```toml
[storage]
backend = "sqlite"

# To use Postgres instead, uncomment and configure:
# backend = "postgres"
# postgres_dsn = "postgres://user:password@host:5432/dbname?sslmode=require"
#
# postgres_dsn supports both libpq URL form (above) and keyword=value form.
# Setting postgres_dsn via the WMAPI_STORAGE__POSTGRES_DSN env var avoids
# committing credentials to the TOML file.
```

> Match the existing comment style in the file; the snippet above is illustrative.

- [ ] **Step 2: Update README**

Append a Plan 10 status entry after the Plan 09 line:

```markdown
- **Plan 10 (Postgres store)** shipped: the daemon now runs against either SQLite (default, dev) or Postgres (production) by setting `[storage] backend = "postgres"` and `postgres_dsn`. The schema and queries are dialect-specific (`internal/store/sqlite/` and `internal/store/postgres/`); a shared test suite (`internal/store/storesuite/`) runs the same assertions against both backends so dialect drift surfaces in CI. Full-text search uses FTS5 on SQLite and `tsvector` + GIN on Postgres — same `/v1/messages/search` contract, dialect-specific ranking. Postgres test packages skip cleanly when Docker is unavailable.
```

Update the trailing line if needed:

```markdown
Docker image and CI workflow land in Plan 11. Outbound message lifecycle events (sent → delivered → read) and group-lifecycle deltas land in a future plan.
```

- [ ] **Step 3: Final repo build + test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: all green.

- [ ] **Step 4: Verify the test suite count parity**

Sanity check: every `RunX` helper in `storesuite/` should be called from both `sqlite/*_test.go` and `postgres/*_test.go`:

```bash
diff \
    <(grep -h "storesuite\.Run" internal/store/sqlite/*_test.go | sort -u) \
    <(grep -h "storesuite\.Run" internal/store/postgres/*_test.go | sort -u)
```

Expected: empty diff. Any mismatch indicates a dropped or extra wrapper.

- [ ] **Step 5: Commit**

```bash
git add README.md config.example.toml
git commit -m "docs: README + config example for Plan 10 Postgres backend"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS (Postgres tests run if Docker available, skip otherwise)
- [ ] Manual smoke: daemon boots against SQLite (default) AND against Postgres (with a real container) — verified in Task 7 Step 3
- [ ] `git log --oneline` shows ~7-8 well-scoped commits
- [ ] Storesuite parity diff (Task 8 Step 4) is empty

When all the above are checked, this plan is complete and v1 is one plan away from done. **Plan 11 (Docker image + docs + examples + CI workflow)** is the final v1 milestone.

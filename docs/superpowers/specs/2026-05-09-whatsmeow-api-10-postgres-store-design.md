# whatsmeow-api Plan 10 — Postgres Store + Integration Tests Against Both Dialects

**Date:** 2026-05-09
**Status:** Design — ready for implementation plan
**Predecessors:** Plans 01–09 shipped on `main`. Storage scaffolding from Plan 03 (config keys, embed.go, migration directory layout) already anticipates a Postgres backend.

## 1. Goals

The daemon already runs against SQLite. Plan 10 adds a parallel Postgres backend, selected at startup via the existing `[storage] backend` config key. The same single binary runs against either dialect with the same HTTP API contract. The store-level test suite runs against both backends so dialect drift surfaces in CI.

This is dialect parity work, not a multi-tenancy or production-deployment plan. Single account per daemon, same `Bundle` shape, same service layer, same HTTP layer.

## 2. Non-goals

- **Multi-tenancy** (`tenant_id` columns, per-row authorization). The Postgres schema is single-tenant; multi-tenancy is a future plan that will need its own schema migration regardless.
- **Postgres-specific tuning.** Connection pool sizing, statement cache, prepared statement strategy — defaults from `pgx` are good enough for a single-account daemon. We can revisit when someone has actual workload pressure.
- **Read replicas, partitioning, sharding.** Out of scope.
- **JSONB / `pg_trgm`-based features.** `events_log.payload` stays opaque TEXT (matches SQLite); messages search uses `tsvector` on Postgres because that's the standard, but `pg_trgm` and other extensions are not introduced.
- **CI workflow updates.** Plan 11's territory.

## 3. Architecture

The current shape (post-Plan 09):

```
service.Service
    └─ store.Bundle ── ChatStore, MessageStore, ContactStore, MediaStore,
                         EventsLog, KV, ReactionStore, ReceiptStore
                       (each implemented in internal/store/sqlite)
```

Plan 10 adds a sibling implementation:

```
service.Service                                  unchanged
    └─ store.Bundle                              unchanged interface
        ├─ internal/store/sqlite (existing)
        └─ internal/store/postgres (NEW)
              ├─ Open(ctx, dsn, logger) (Bundle, error)
              ├─ chats.go, contacts.go, ...     mirror sqlite/, query-for-query
              └─ uses pgx/v5/stdlib via database/sql
```

Migrations:

```
internal/store/migrations/
    ├─ embed.go             modified — adds Postgres() function
    ├─ sqlite/              existing — three migration sets
    └─ postgres/            NEW — three migration sets, Postgres-native SQL
```

Bundle selection happens once at `cmd/whatsmeow-api/serve.go` startup, via a switch on `cfg.Storage.Backend`. The rest of the daemon is dialect-agnostic.

## 4. Storage configuration

Existing keys (no change to public surface):

```toml
[storage]
backend       = "sqlite"      # or "postgres"
postgres_dsn  = ""             # required when backend = "postgres"

data_dir = "./data"            # SQLite-specific; ignored when backend = "postgres"
```

The validation already in `internal/config/config.go` rejects an empty `postgres_dsn` when `backend == "postgres"`. No new config keys in this plan.

## 5. Schema — Postgres dialect

The three migration sets correspond 1-for-1 with the existing SQLite migrations. Each is rewritten in Postgres-native SQL. The Go-level `store.Bundle` interface and domain types are unchanged; only the on-disk shape differs.

### 5.1 Migration `0001_init` (Postgres)

Tables in the Postgres `0001_init.up.sql`:

- **chats** — `jid TEXT PRIMARY KEY`, `name TEXT NOT NULL DEFAULT ''`, `kind TEXT NOT NULL`, `last_msg_at TIMESTAMPTZ`, `unread_count INTEGER NOT NULL DEFAULT 0`, `archived BOOLEAN NOT NULL DEFAULT FALSE`, `pinned BOOLEAN NOT NULL DEFAULT FALSE`, `muted_until TIMESTAMPTZ`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. Indexes on `last_msg_at DESC` and `archived`.
- **messages** — `id TEXT PRIMARY KEY`, `chat_jid TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE`, `sender_jid TEXT NOT NULL`, `kind TEXT NOT NULL`, `body TEXT NOT NULL DEFAULT ''`, `timestamp TIMESTAMPTZ NOT NULL`, `is_from_me BOOLEAN NOT NULL`, `reply_to_id TEXT`, `edited_at TIMESTAMPTZ`, `deleted_at TIMESTAMPTZ`, `body_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(body, ''))) STORED`. Indexes: `(chat_jid, timestamp DESC)`, `GIN(body_tsv)`.
- **contacts** — `jid TEXT PRIMARY KEY`, `push_name TEXT NOT NULL DEFAULT ''`, `business_name TEXT NOT NULL DEFAULT ''`, `verified_name TEXT NOT NULL DEFAULT ''`, `last_seen TIMESTAMPTZ`. Index on `push_name` for prefix search.
- **media** — `message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE`, `mime TEXT NOT NULL`, `size BIGINT NOT NULL`, `sha256 TEXT NOT NULL`, `path TEXT NOT NULL`, `caption TEXT NOT NULL DEFAULT ''`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
- **events_log** — `seq BIGSERIAL PRIMARY KEY`, `time TIMESTAMPTZ NOT NULL`, `type TEXT NOT NULL`, `payload TEXT NOT NULL`. Index on `seq` (already PK), no other indexes needed.
- **kv** — `key TEXT PRIMARY KEY`, `value TEXT NOT NULL`.

The `0001_init.down.sql` drops the tables in reverse FK order.

> Note: column names follow the existing SQLite shape. If the SQLite schema uses different column names (e.g. `created_at` is `created` somewhere), the Postgres migration must match exactly so the Go code can use the same SQL.

The actual column inventory is what's currently in `internal/store/migrations/sqlite/0001_init.up.sql`; the implementation plan re-reads that file before writing the Postgres mirror to lock down exact names and types.

### 5.2 Migration `0002_reactions` (Postgres)

- **reactions** — `message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE`, `sender_jid TEXT NOT NULL`, `emoji TEXT NOT NULL`, `timestamp TIMESTAMPTZ NOT NULL`, `PRIMARY KEY (message_id, sender_jid)`. Index on `message_id` already covered by the PK.

### 5.3 Migration `0003_receipts` (Postgres)

- **receipts** — `message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE`, `reader_jid TEXT NOT NULL`, `chat_jid TEXT NOT NULL`, `type TEXT NOT NULL`, `timestamp TIMESTAMPTZ NOT NULL`, `PRIMARY KEY (message_id, reader_jid, type)`.

### 5.4 Type-mapping reference

| Go domain field | SQLite column | Postgres column |
|---|---|---|
| `time.Time` | `INTEGER` (Unix seconds) | `TIMESTAMPTZ` |
| `bool` | `INTEGER` (0/1) | `BOOLEAN` |
| `int64` (id, count) | `INTEGER` | `BIGINT` / `BIGSERIAL` |
| `string` (JID, body, etc.) | `TEXT` | `TEXT` |
| `[]byte` (none currently used in store) | `BLOB` | `BYTEA` |

The Postgres impl binds `time.Time` directly via pgx; no intermediate Unix-seconds round-trip. The shared test suite (§7) asserts equality after rounding to seconds so SQLite's lower precision doesn't cause flakes.

### 5.5 FTS divergence

Per the brainstorm decision, full-text search is dialect-specific:

- **SQLite**: existing FTS5 virtual table `messages_fts` (no change).
- **Postgres**: a `body_tsv` generated `tsvector` column on `messages` with a GIN index. The `MessageStore.Search(ctx, q, limit)` Postgres impl uses:
  ```sql
  SELECT id, chat_jid, sender_jid, kind, body, timestamp, ...
  FROM messages
  WHERE body_tsv @@ plainto_tsquery('simple', $1)
  ORDER BY ts_rank(body_tsv, plainto_tsquery('simple', $1)) DESC, timestamp DESC
  LIMIT $2;
  ```
  `plainto_tsquery` accepts free-form user input safely (treats it as ANDed terms). Using `'simple'` as the dictionary keeps stemming off, which best matches FTS5's stemming-off default behavior.

The HTTP `/v1/messages/search` contract is unchanged: same query string, same response shape, dialect-specific ranking. The README documents that ranking algorithms differ.

## 6. Go-level structure

### 6.1 New package `internal/store/postgres`

Files mirror `internal/store/sqlite/`:

| sqlite/ | postgres/ | Notes |
|---|---|---|
| `store.go` | `store.go` | `Open(ctx, dsn, logger) (Bundle, error)`; opens `*sql.DB` via `pgx/v5/stdlib`; runs migrations; returns `Bundle` |
| `chats.go` | `chats.go` | `chatStore` impl of `store.ChatStore` |
| `contacts.go` | `contacts.go` | |
| `events_log.go` | `events_log.go` | |
| `kv.go` | `kv.go` | |
| `media.go` | `media.go` | |
| `messages.go` | `messages.go` | + dialect-specific Search() |
| `reactions.go` | `reactions.go` | |
| `receipts.go` | `receipts.go` | |
| `time.go` | (likely unused) | SQLite has Unix-seconds conversions; Postgres binds `time.Time` natively |
| `*_test.go` | `*_test.go` | One test per store interface, mirroring SQLite tests |

Each Postgres `*.go` file is a near-line-for-line port of its SQLite sibling, with these mechanical changes:

1. **Placeholders**: `?` → `$1, $2, ...`. The pgx driver requires positional placeholders.
2. **Time binding**: `time.Time` flows directly through the driver in both directions; no `unixSeconds` helper.
3. **Boolean binding**: `bool` works natively; no `0/1` integer literal.
4. **Returning ID**: `RETURNING seq` works on both dialects (SQLite ≥3.35), but the Postgres equivalent for events_log Append uses `RETURNING seq` to get the BIGSERIAL value.
5. **ON CONFLICT**: clause shape is identical between SQLite and Postgres for the use cases we have. Verify per query.
6. **`ts_rank`/`tsvector` query** in `messages.go`'s Search.

The package's `Open` returns a `store.Bundle` — same interface, different impl, completely transparent to callers.

### 6.2 Driver

`github.com/jackc/pgx/v5/stdlib` registers a `pgx` driver for `database/sql`. We open as:

```go
db, err := sql.Open("pgx", dsn)
```

This keeps the same `*sql.DB` API surface as the SQLite store. No abstraction layer, no per-dialect query interface in Go — the dialect-specific code is purely in the SQL strings.

`pgx`'s direct-mode driver (`pgxpool`) is more idiomatic for new Postgres code, but using `database/sql` keeps the codebase shape consistent with the SQLite path. We can switch to `pgxpool` later if profiling shows real wins.

### 6.3 Migrations runner

`internal/store/migrations/embed.go` gains:

```go
//go:embed postgres/*.sql
var postgresFiles embed.FS

// Postgres returns the embedded Postgres migration files rooted so that file
// names look like "0001_init.up.sql". Mirrors SQLite().
func Postgres() fs.FS {
    sub, err := fs.Sub(postgresFiles, "postgres")
    if err != nil {
        panic(err)
    }
    return sub
}
```

`internal/store/postgres/store.go`'s `Open` calls `migrate.NewWithSourceInstance(...)` against the iofs source built from `migrations.Postgres()`, with the `pgx` database driver. Same shape as the SQLite path's migration call.

## 7. Testing

### 7.1 testcontainers-go for Postgres tests

New module dependency: `github.com/testcontainers/testcontainers-go` (and its `postgres` module if convenient — the bare module is fine too).

A shared test helper in `internal/store/postgres/testutil.go`:

```go
// NewTestPostgres starts a postgres:16-alpine container, runs the daemon's
// migrations, and returns a connected Bundle plus a t.Cleanup that tears the
// container down. Skips the calling test when Docker is unavailable.
func NewTestPostgres(t *testing.T) (store.Bundle, func()) { ... }
```

`TestMain` in each Postgres `*_test.go` file (or a single shared `TestMain` in a small test-helper file) is the natural place to do the Docker check once and either run the suite or skip with a single message. The check is cheap: one `exec.Command("docker", "info")` call gated by a `sync.Once`.

If Docker is unavailable, every Postgres test calls `t.Skip("postgres tests require docker")`. Build + vet + the SQLite suite still pass for non-Docker contributors.

For speed: one container per test package, shared across all tests in that package. Per-test isolation is obtained by deleting all rows before each test (cheap on an in-container DB) or by wrapping each test in a transaction that rolls back. The implementation plan picks one and applies it consistently.

### 7.2 Test parity

Every existing SQLite store test should have an equivalent Postgres test. Two viable patterns:

**Pattern A — duplicated tests.** `chats_test.go` in both packages, with the same assertions but differing test setup (sqlite in-memory vs. testcontainer). Pro: each package is self-contained, easy to read. Con: drift risk over time.

**Pattern B — shared suite helper.** Move test bodies into a new `internal/store/storesuite` package as `func RunChatStoreSuite(t *testing.T, s store.ChatStore)` etc. Each dialect's `*_test.go` file calls the shared suite + its own bundle constructor. Pro: zero drift, one source of truth. Con: a refactor in addition to the port.

The implementation plan should pick **Pattern B** if the SQLite tests are mostly black-box (operate on the public store interface). If they reach into SQLite-specific internals (e.g. inspect raw rows via SQL), keep them as-is and use Pattern A for the new Postgres tests.

> Open question deferred to impl plan: which pattern. The reviewer's recommendation is B if feasible, A otherwise.

### 7.3 Test environment

- `go test ./internal/store/postgres/...` — runs the full Postgres suite against a testcontainer.
- `go test ./...` — full repo suite. Postgres package tests are part of this; if Docker is unavailable they skip gracefully.
- CI: any Linux runner with Docker (GitHub Actions default) runs both SQLite and Postgres suites. CI integration is Plan 11; this plan only ensures the local tests work.

## 8. Wiring `cmd/whatsmeow-api/serve.go`

Today the serve path looks roughly like:

```go
bundle, err := sqlite.Open(ctx, cfg.DataDir, logger)
```

After Plan 10:

```go
var bundle store.Bundle
switch cfg.Storage.Backend {
case "sqlite":
    bundle, err = sqlite.Open(ctx, cfg.DataDir, logger)
case "postgres":
    bundle, err = postgres.Open(ctx, cfg.Storage.PostgresDSN, logger)
default:
    return fmt.Errorf("unsupported storage.backend: %q", cfg.Storage.Backend)
}
```

Config validation already gates this in `config.go`'s `Validate()`, so the `default` arm is defensive and unreachable in practice.

The bundle is then handed to `service.New(...)` and `httpapi.NewServer(...)` exactly as today. No service or HTTP changes.

## 9. Documentation

- **README**: a Plan 10 status entry explaining how to switch backends. Copy the config snippet from §4.
- **config.example.toml**: a commented-out Postgres example, e.g.:
  ```toml
  # [storage]
  # backend       = "postgres"
  # postgres_dsn  = "postgres://user:pass@localhost:5432/whatsmeow_api?sslmode=disable"
  ```
- **No new top-level docs.** The master design doc already covers the architecture; Plan 10's spec + plan are the artifact for maintainers.

## 10. Risks and trade-offs

1. **Schema drift between dialects.** Two migration sets means contributors can update one and forget the other. Mitigation: shared test suite (§7.2 Pattern B) — any Go-level behavior that exercises the schema runs against both. Optional follow-up: a meta-test that introspects both schemas and asserts column lists match per table; nice but not required for v1.

2. **Time precision differences.** SQLite Unix-seconds (1s precision) vs. Postgres TIMESTAMPTZ (microseconds). The shared test suite must round to seconds at assertion boundaries. The Go domain types are `time.Time` either way; the Postgres impl preserves microseconds in storage but loses them on round-trip through SQLite — acceptable for our use case.

3. **FTS contract drift.** A search query that returns N results on SQLite might return N±k on Postgres (or in a different order). The shared test asserts "non-empty result for matching query" rather than exact ordering. The README documents this.

4. **testcontainers startup overhead.** ~3-5s per test package. Acceptable. If the Postgres test suite grows beyond 3-4 packages, reuse mode (`Reuse: true`) can amortize. Not a v1 concern.

5. **`pg_trgm` not used.** Some users may want fuzzy/typo-tolerant search; FTS5 and tsvector both stem strictly. We document the limitation; pg_trgm can land in a follow-up plan.

6. **DSN-in-config secret hygiene.** `postgres_dsn` includes a password. Mitigation: existing config also supports `WMAPI_*` env-var overrides per Plan 01; users in shared environments set `WMAPI_STORAGE__POSTGRES_DSN` instead of putting it in TOML. Not new to this plan; flag in README.

## 11. Open questions for the implementation plan (not blockers for spec)

- Whether to use **Pattern A or Pattern B** for test parity (§7.2). The plan picks one after spotting how black-box the existing SQLite tests are.
- Whether SQLite's existing `_test.go` files reach into dialect-specific internals (raw SQL probes). If yes, those tests stay as-is and Postgres gets parallel implementations.
- Exact column inventory of the existing SQLite migrations — the plan re-reads `0001_init.up.sql`, `0002_reactions.up.sql`, `0003_receipts.up.sql` and locks down the Postgres mirror.
- Whether to add a `pgxpool`-based future path — out of scope; documented for future plan.

## 12. Approval gate

Estimated 8 implementation tasks:

1. Postgres migration files (3 sets) + `migrations/embed.go` `Postgres()` function.
2. `internal/store/postgres/store.go` (`Open`, `Bundle`, migration runner) + `testutil.go` with `NewTestPostgres`/Docker-skip + a single passing smoke test.
3. Decide and apply test-parity pattern (A or B). If B: extract `storesuite` package and convert SQLite tests to call it. If A: just port test files.
4. Port `chats.go`, `contacts.go`, `kv.go` (no FTS, no FK trickiness) + tests.
5. Port `messages.go` with `tsvector` Search + tests including FTS-parity assertion.
6. Port `media.go`, `reactions.go`, `receipts.go`, `events_log.go` + tests.
7. Wire `cmd/whatsmeow-api/serve.go` dialect switch + manual smoke against a Postgres container (daemon-level, not just store-level).
8. README + `config.example.toml` docs + final smoke + commit hygiene.

Date: 2026-05-09.

# whatsmeow-api Plan 02 — waclient + Login Design

**Date:** 2026-04-30
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 01 (Foundations) — merged.

## 1. Purpose

Wire `whatsmeow` into the daemon so it can pair with a WhatsApp account, expose login + logout + real `/v1/status` over the HTTP API, and ship CLI subcommands that drive those endpoints. After Plan 02 the daemon can be paired and stay logged in across restarts; subsequent plans (03-08) build storage and messaging on top of the connection this plan establishes.

## 2. Goals

- One package (`internal/waclient`) owns all `whatsmeow` integration; everything else depends only on a `WAClient` interface.
- Login is a streaming experience: QR codes rotate, pairing terminates with success/error/timeout. Both QR and phone-pair use SSE for symmetry.
- The daemon auto-resumes a saved session at startup; explicit `/v1/login/*` is required only when no session exists.
- Logout matches whatsmeow's own semantics: `Client.Logout()` invalidates server-side, daemon stays running.
- The CLI dogfoods the API — every login/status/logout subcommand is a thin HTTP client of the daemon.

## 3. Non-goals (Plan 02)

- App-level chat/message storage — Plan 03.
- Sending/receiving messages — Plan 04.
- Generalized SSE for incoming messages — Plan 09; this plan ships the minimum SSE machinery needed for login and Plan 09 will generalize it.
- TLS / remote deployment hardening — defer until ops needs it.

## 4. Architecture

```
                     ┌─────────────────────────────────┐
                     │  cmd/whatsmeow-api              │
                     │  (cobra: serve | login | …)     │
                     └────────────────┬────────────────┘
                                      │
        serve subcommand              │           login/status/logout subcommands
                                      │           (HTTP+SSE client of the daemon)
                                      ▼                              │
        ┌────────────────────┐  ┌──────────────────┐                │
        │ internal/transport │◄─│ internal/service │                │
        │   /http (handlers) │  │ (use cases)      │                │
        └─────────┬──────────┘  └─────────┬────────┘                │
                  │                       │                         ▼
                  │                       ▼              ┌────────────────────┐
                  │            ┌────────────────────┐    │ internal/client    │
                  │            │ internal/waclient  │    │ (HTTP+SSE client)  │
                  │            │ (WAClient adapter) │    └─────────┬──────────┘
                  │            └─────────┬──────────┘              │
                  │                      ▼                          │
                  │            ┌────────────────────┐               │
                  │            │ go.mau.fi/whatsmeow │               │
                  │            │ + whatsmeow/sqlstore│               │
                  │            └─────────┬──────────┘               │
                  │                      ▼                          │
                  │            ┌────────────────────┐               │
                  │            │ data_dir/whatsmeow │               │
                  │            │ -session.db (or PG)│               │
                  │            └────────────────────┘               │
                  ▼                                                  │
            HTTP/JSON ◄──────────────── HTTP/JSON over loopback ────┘
            (Plan 01)                   (CLI client subcommands)
```

### 4.1 Package responsibilities

- **`internal/waclient`** — only package importing `whatsmeow`. Owns the `*whatsmeow.Client`, registers a single event handler that updates `lastConnectedAt`, holds a `sync.Mutex` to serialize login attempts. Exposes the `WAClient` interface.
- **`internal/service`** — use-case methods (`Status`, `LoginQR`, `LoginPhone`, `Logout`). Pass-through to `WAClient` in this plan, but the seam matters: Plan 04+ will mix in `Store` calls behind the same interface.
- **`internal/transport/http`** — handlers for `/v1/status`, `/v1/login/qr`, `/v1/login/phone`, `/v1/logout`. SSE framing helpers live here too.
- **`internal/client`** — small library wrapping the daemon's HTTP+SSE API. Used by CLI subcommands; reusable by future tooling and tests.
- **`cmd/whatsmeow-api`** — adds `login qr|phone`, `status`, `logout` subcommands.

## 5. waclient interface

```go
type WAClient interface {
    Status() Status
    Resume(ctx context.Context) error
    LoginQR(ctx context.Context) (<-chan QREvent, error)
    LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
    Logout(ctx context.Context) error
    Close() error
}

type Status struct {
    Connected bool       // IsConnected() && IsLoggedIn()
    JID       *string    // canonical JID, e.g. "27821234567@s.whatsapp.net"
    PushName  *string
    Since     *time.Time // when current connection was established
}

type QREvent struct {
    Code     string  // empty when Terminal
    Terminal bool
    Outcome  string  // "" while streaming; "success" | "timeout" | "err-client-outdated" | "err-..." when Terminal
}

type PairEvent struct {
    Code     string  // 8-char pair code on the first event; empty thereafter
    Terminal bool
    Outcome  string  // "" until terminal
}
```

Lifecycle rules:
- `Resume` is idempotent. If no device is stored it returns `nil` and leaves `Connected=false`.
- `LoginQR` and `LoginPhone` are mutually exclusive: a second concurrent call returns `ErrLoginInProgress`.
- The returned channel is closed after the terminal event is delivered.
- Cancelling `ctx` cancels the in-progress login and emits a terminal event with `Outcome="ctx-cancelled"`.
- `Logout` returns `ErrNotLoggedIn` if the client isn't currently logged in.
- `Close` is called on daemon shutdown; disconnects without logging out.

## 6. whatsmeow session store

Both backends use `whatsmeow/store/sqlstore` directly:

| backend  | DSN passed to sqlstore                                                   | location                                |
|----------|--------------------------------------------------------------------------|------------------------------------------|
| sqlite   | `file:<data_dir>/whatsmeow-session.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)` | `<data_dir>/whatsmeow-session.db`       |
| postgres | `<storage.postgres_dsn>` (unmodified)                                    | `whatsmeow_session` schema, auto-created |

For Postgres, daemon startup opens a `*sql.DB` against `storage.postgres_dsn`, runs `CREATE SCHEMA IF NOT EXISTS whatsmeow_session` and `SET search_path TO whatsmeow_session, public`, and passes that already-configured `*sql.DB` to `sqlstore.NewWithDB`. (No DSN string mutation — keeps the DSN canonical for the app store in Plan 03 and matches whatsmeow's documented "bring your own DB" pattern.)

`<data_dir>` is created at startup if missing (Plan 01 final-review feedback turned into action here). Mode `0o750`.

## 7. HTTP API

All endpoints are auth-gated and respect the bearer-token middleware from Plan 01.

### 7.1 GET /v1/status (replaces the Plan 01 placeholder)

Response: `200 application/json`
```json
{
  "wa_connected": true,
  "jid": "27821234567@s.whatsapp.net",
  "push_name": "Askar",
  "since": "2026-04-30T11:23:45Z"
}
```
When disconnected: `wa_connected: false`, the other three fields are `null`.

### 7.2 POST /v1/login/qr

Response: `200 text/event-stream`. SSE event names:
- `qr` — `data` is `{"code":"2@x...,abc...","expires_in_s":20}` where `code` is the raw QR string from whatsmeow (a `2@...` opaque payload that the official WhatsApp app knows how to parse). Emitted each time whatsmeow rotates. The CLI renders this string into a scannable QR via `qrterminal`; alternative clients can encode it however they like.
- `connection` — `data` is `{"outcome":"success"|"timeout"|"err-..."}`. Always the last event; the stream is closed afterward.

If a login is already in progress: `409 application/problem+json` with `code=wa.login_in_progress`.

If already logged in: `409` with `code=wa.already_logged_in`.

Heartbeat: SSE comment line every 15s while waiting on rotation events.

### 7.3 POST /v1/login/phone

Request: `application/json` body `{"phone_number":"+27821234567"}`.

Response: `200 text/event-stream`. SSE event names:
- `pair_code` — `data` is `{"code":"ABCD-1234","expires_in_s":120}`. Always the first event.
- `connection` — `data` is `{"outcome":"success"|"timeout"|"err-..."}`. Last event.

Validation:
- Phone number must be E.164 (`+` followed by 6-15 digits). 400 with `code=request.invalid` on failure.
- Same `409` semantics as `/v1/login/qr`.

### 7.4 POST /v1/logout

No body. Responses:
- `204 No Content` on success.
- `409 application/problem+json` `code=wa.not_logged_in` if not currently logged in.

## 8. Auto-resume on `serve`

After config load and logger init in `serveCmd`:

```
1. Ensure data_dir exists (MkdirAll, mode 0750)
2. Build the whatsmeow session store (sqlite or postgres per config)
3. waclient.New(store, logger) -> WAClient
4. waclient.Resume(ctx)         (logs "no session" if none, else "connected as <jid>")
5. service := service.New(waclient, ...)
6. httpapi.NewServer(Deps{Config, Logger, Service: service}).Run(ctx)
7. On shutdown: srv.Run returns -> waclient.Close()
```

Resume failures other than "no session" are logged at warn level but do not exit the daemon — the operator can call `/v1/login/*` to re-pair.

## 9. CLI client subcommands

All subcommands resolve their daemon URL and token from:

1. CLI flags `--url`, `--token`
2. Env vars `WMAPI_URL`, `WMAPI_TOKEN`
3. Defaults: `http://127.0.0.1:8080`, no token

### 9.1 `whatsmeow-api login qr`

1. Calls `client.LoginQR(ctx)` returning a channel of QR events.
2. For each `qr` event, clears the screen and renders the code via `qrterminal.Generate(code, qrterminal.M, os.Stdout)` plus a one-line "expires in 20s, scan with your phone" hint.
3. On `connection` terminal event, prints `"logged in as <jid>"` and exits 0 on success, exits 1 with the outcome on failure.

### 9.2 `whatsmeow-api login phone <number>`

1. Validates the number is E.164.
2. Calls `client.LoginPhone(ctx, number)`.
3. Prints `"Pair code: ABCD-1234   Open WhatsApp → Settings → Linked Devices → Link with phone number → enter the code above (expires in 2 min)"`.
4. Waits for terminal event; exits as in `login qr`.

### 9.3 `whatsmeow-api status`

GETs `/v1/status` and prints:
- Connected: `connected as 27821234567@s.whatsapp.net (Askar) since 2026-04-30T11:23:45Z`
- Disconnected: `not connected`
- Daemon unreachable: `daemon unreachable: <error>` and exit 1.

### 9.4 `whatsmeow-api logout`

POSTs `/v1/logout`. Prints `logged out`, or `not logged in`, or daemon-unreachable error. Exit code 0 on success or "not logged in"; 1 on transport error.

## 10. Service layer

```go
type Service interface {
    Status(ctx context.Context) (waclient.Status, error)
    LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
    LoginPhone(ctx context.Context, number string) (<-chan waclient.PairEvent, error)
    Logout(ctx context.Context) error
}
```

Plan 02's implementation is pass-through to `WAClient`. The seam exists so Plan 04+ can layer message-store interactions in without touching handlers.

## 11. Deps extension

```go
type Deps struct {
    Config  config.Config
    Logger  *slog.Logger
    Service service.Service     // NEW
}
```

Handlers depend on `Service`, not on `WAClient` directly. Tests pass a fake `Service`.

## 12. Errors over the wire (problem+json codes)

| code                       | when                                          | http |
|----------------------------|-----------------------------------------------|-----:|
| `request.invalid`          | bad phone number, malformed JSON              | 400  |
| `auth.unauthorized`        | (Plan 01) bad bearer token                    | 401  |
| `wa.already_logged_in`     | login attempt while connected                 | 409  |
| `wa.not_logged_in`         | logout while disconnected                     | 409  |
| `wa.login_in_progress`     | concurrent login attempt                      | 409  |
| `wa.login_failed`          | the SSE upgrade succeeded but the daemon failed to start the login flow before the first event (e.g. whatsmeow returned an error from `GetQRChannel`). For all other failure modes the terminal `connection` SSE event carries the outcome string. | 500 |

## 13. Testing strategy

- **waclient adapter:** the thin part importing whatsmeow is excluded from coverage gates. Pure helpers (status formatting, JID nil-vs-string handling, event translation) live in their own file and have unit tests.
- **service:** unit-tested with a fake `WAClient` covering: status pass-through, LoginQR returning channel from waclient, LoginPhone with bad number → propagates error, Logout error wrapping.
- **HTTP handlers:** `httptest` round-trips against a fake `Service`. SSE tests assert: event names, JSON payload shape, terminal event closes the response, heartbeat comments don't break clients.
- **internal/client:** spins up an `httptest.Server` returning canned SSE bytes; verifies the parser emits the right `QREvent` / `PairEvent` sequence and closes the channel after terminal events.
- **CLI subcommands:** small round-trip tests using a fake daemon (`internal/client` stays the unit; CLI is glue).
- **End-to-end pairing:** manual smoke with a real account before declaring Plan 02 done.

## 14. Project layout additions

```
internal/
├── waclient/
│   ├── waclient.go             # types + interface + helpers
│   ├── waclient_test.go        # helper tests
│   └── whatsmeow_adapter.go    # only file importing whatsmeow
├── service/
│   ├── service.go              # Service interface + New(...)
│   ├── login.go                # implementations (also Status, Logout)
│   └── login_test.go
├── client/
│   ├── client.go               # daemon HTTP+SSE client
│   └── client_test.go
└── transport/http/
    ├── status.go               # rewrites placeholder
    ├── status_test.go          # rewrites placeholder test
    ├── login_qr.go
    ├── login_qr_test.go
    ├── login_phone.go
    ├── login_phone_test.go
    ├── logout.go
    ├── logout_test.go
    ├── sse.go                  # small SSE writer helper (event/data/comment)
    └── sse_test.go
cmd/whatsmeow-api/
├── login.go                    # `login qr` and `login phone <number>`
├── status.go                   # `status`
└── logout.go                   # `logout`
```

The placeholder `internal/{service,waclient}/doc.go` stubs are replaced by the real files in this plan.

## 15. Dependencies (added in Plan 02)

| module                                | reason                                 |
|---------------------------------------|----------------------------------------|
| `go.mau.fi/whatsmeow`                 | the protocol client itself             |
| `modernc.org/sqlite`                  | pure-Go SQLite driver registered with `database/sql` so `sqlstore.New("sqlite", ...)` works without CGo |
| `github.com/jackc/pgx/v5/stdlib`      | Postgres driver registered with `database/sql` |
| `github.com/mdp/qrterminal/v3`        | renders the QR in the CLI              |

`whatsmeow/store/sqlstore` and `go.mau.fi/util/dbutil` are transitive — no separate `go get` needed.

## 16. Open questions deferred

- Should the SSE login endpoints support `Last-Event-ID` resume? Probably not — pair flows are short-lived; defer to Plan 09 if a real need surfaces.
- Should `/v1/login/qr` accept a `?format=svg|terminal` query so CLI can render whatsmeow's raw QR string vs a pre-rendered image? For Plan 02 the SSE event carries the raw string and the CLI does its own rendering; image rendering is a future nice-to-have.
- Whether to expose presence (online/typing) push from the daemon during pairing so the CLI can show "phone connected, waiting for confirmation". Defer.

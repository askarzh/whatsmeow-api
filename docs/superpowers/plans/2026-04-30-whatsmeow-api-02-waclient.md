# whatsmeow-api Plan 02 — waclient + Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `whatsmeow` into the daemon behind a `WAClient` interface, ship SSE-driven QR + phone-pair login + logout endpoints + the real `/v1/status`, auto-resume saved sessions on `serve`, and add `whatsmeow-api login|status|logout` CLI subcommands that drive the daemon over its own API.

**Architecture:** A single package (`internal/waclient`) owns all whatsmeow integration; everything else depends on the `WAClient` interface. The HTTP handlers stream login progress via Server-Sent Events. A small `internal/client` package wraps the daemon's HTTP+SSE API and is consumed by the CLI subcommands. `serveCmd` constructs the whatsmeow session store, builds the waclient, attempts a resume, then starts the HTTP server.

**Tech Stack:**
- Go 1.26
- `go.mau.fi/whatsmeow` — WhatsApp protocol client
- `go.mau.fi/whatsmeow/store/sqlstore` — session/key storage (SQLite or Postgres)
- `modernc.org/sqlite` — pure-Go SQLite driver registered with `database/sql`
- `github.com/jackc/pgx/v5/stdlib` — Postgres driver registered with `database/sql`
- `github.com/mdp/qrterminal/v3` — terminal QR rendering for the CLI
- All Plan 01 stack (chi, cobra, koanf, slog, testify)

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/waclient/waclient.go` | Domain types (`Status`, `QREvent`, `PairEvent`), `WAClient` interface, sentinel errors, small pure helpers (phone-number validation, JID formatting). |
| `internal/waclient/waclient_test.go` | Unit tests for helpers. |
| `internal/waclient/whatsmeow_adapter.go` | The only file importing `whatsmeow`. Implements `WAClient` against `*whatsmeow.Client`. |
| `internal/service/service.go` | `Service` interface, `New(WAClient) Service` constructor, pass-through implementation. |
| `internal/service/service_test.go` | Tests against a fake `WAClient`. |
| `internal/transport/http/sse.go` | Tiny SSE writer helper: `WriteEvent(w, name, payload)`, `WriteHeartbeat(w)`, sets Content-Type and flushes. |
| `internal/transport/http/sse_test.go` | Round-trip test using `httptest.ResponseRecorder` + a flushable wrapper. |
| `internal/transport/http/status.go` | Rewritten — uses `Service.Status()`. |
| `internal/transport/http/status_test.go` | Rewritten — uses fake `Service`. |
| `internal/transport/http/login_qr.go` | `LoginQRHandler(svc)`. |
| `internal/transport/http/login_qr_test.go` | SSE event-shape + framing tests. |
| `internal/transport/http/login_phone.go` | `LoginPhoneHandler(svc)` + body parsing. |
| `internal/transport/http/login_phone_test.go` | Validation + SSE shape tests. |
| `internal/transport/http/logout.go` | `LogoutHandler(svc)` returning 204 / 409. |
| `internal/transport/http/logout_test.go` | Both code paths. |
| `internal/transport/http/router.go` | Modified — adds the four routes, wires `Service` into handlers. |
| `internal/transport/http/router_test.go` | Modified — auth-gated routes get coverage. |
| `internal/client/client.go` | HTTP+SSE client of the daemon (`Status`, `LoginQR`, `LoginPhone`, `Logout`). |
| `internal/client/client_test.go` | Round-trip tests against `httptest.Server` returning canned SSE/JSON. |
| `cmd/whatsmeow-api/status.go` | `status` subcommand. |
| `cmd/whatsmeow-api/logout.go` | `logout` subcommand. |
| `cmd/whatsmeow-api/login.go` | `login qr` and `login phone <number>` subcommands (uses qrterminal). |
| `cmd/whatsmeow-api/main.go` | Modified — registers the new subcommands. |
| `cmd/whatsmeow-api/serve.go` | Modified — ensures `data_dir`, builds session store, builds waclient, calls Resume, builds Service, passes into Deps. |
| `internal/transport/http/router.go` (Deps) | Modified — `Service` field added; `Logger` and `Config` retained. |
| `README.md` | Status section updated. |

Files removed: `internal/waclient/doc.go` (replaced by waclient.go's package doc), `internal/service/doc.go` (replaced by service.go's package doc).

---

## Task 1: Add Plan 02 dependencies

**Files:** `go.mod`, `go.sum`

- [ ] **Step 1: Add the runtime dependencies**

```bash
cd /home/askar/src/whatsmeow-api
go get go.mau.fi/whatsmeow@latest
go get modernc.org/sqlite@latest
go get github.com/jackc/pgx/v5@latest
go get github.com/mdp/qrterminal/v3@latest
```

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
git commit -m "deps: add whatsmeow, modernc/sqlite, pgx, qrterminal"
```

---

## Task 2: waclient types, interface, and helpers

**Files:**
- Create: `internal/waclient/waclient.go` (replace `internal/waclient/doc.go`)
- Test: `internal/waclient/waclient_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/waclient/waclient_test.go`:
```go
package waclient_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
)

func TestValidatePhoneNumber(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"+27821234567", true},
		{"+1234567", true},
		{"+123456789012345", true},
		{"27821234567", false},        // missing +
		{"+abc12345", false},          // non-digit
		{"+", false},                  // empty digits
		{"+12345", false},             // too short (under 6 digits)
		{"+1234567890123456", false},  // too long (over 15 digits)
		{" +27821234567", false},      // leading space
		{"+27 821 234 567", false},    // spaces
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, waclient.IsValidPhoneNumber(tc.in))
		})
	}
}

func TestErrorsExist(t *testing.T) {
	assert.NotNil(t, waclient.ErrLoginInProgress)
	assert.NotNil(t, waclient.ErrAlreadyLoggedIn)
	assert.NotNil(t, waclient.ErrNotLoggedIn)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/waclient/...`
Expected: FAIL — package doesn't compile.

- [ ] **Step 3: Write the implementation**

First, remove the stub:
```bash
git rm internal/waclient/doc.go
```

Create `internal/waclient/waclient.go`:
```go
// Package waclient is the only package that imports whatsmeow. It owns the
// *whatsmeow.Client, registers event handlers, and translates whatsmeow types
// into the domain types used by the rest of the daemon.
package waclient

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// Status is the daemon's view of the current WhatsApp connection.
type Status struct {
	Connected bool
	JID       *string
	PushName  *string
	Since     *time.Time
}

// QREvent is one frame of the QR-login stream. While streaming, Code is set
// and Terminal is false. The final event has Terminal=true and Outcome set.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string // "success" | "timeout" | "err-..." | "ctx-cancelled"
}

// PairEvent is one frame of the phone-pair-login stream. The first event
// carries Code (the 8-char pairing code). The terminal event has Terminal=true.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// WAClient is the abstraction over whatsmeow used by the rest of the daemon.
type WAClient interface {
	Status() Status
	Resume(ctx context.Context) error
	LoginQR(ctx context.Context) (<-chan QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
	Logout(ctx context.Context) error
	Close() error
}

// Sentinel errors so callers can distinguish failure modes without parsing strings.
var (
	ErrLoginInProgress = errors.New("waclient: login already in progress")
	ErrAlreadyLoggedIn = errors.New("waclient: already logged in")
	ErrNotLoggedIn     = errors.New("waclient: not logged in")
)

var phoneRE = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// IsValidPhoneNumber checks that s looks like an E.164 number (a leading '+'
// followed by 6-15 digits, no spaces or punctuation).
func IsValidPhoneNumber(s string) bool {
	return phoneRE.MatchString(s)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/waclient/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/waclient_test.go
git commit -m "waclient: types, interface, and phone-number validator"
```

---

## Task 3: whatsmeow adapter

**Files:**
- Create: `internal/waclient/whatsmeow_adapter.go`

There is no automated test for this file — it depends on a real WhatsApp connection. Coverage comes from the manual smoke test (Task 18) and from the `service` and HTTP handler tests using a fake `WAClient`.

- [ ] **Step 1: Write the adapter**

Create `internal/waclient/whatsmeow_adapter.go`:
```go
package waclient

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Adapter is the production WAClient backed by *whatsmeow.Client.
type Adapter struct {
	container *sqlstore.Container
	logger    *slog.Logger

	mu              sync.Mutex
	client          *whatsmeow.Client
	loginInProgress bool
	lastConnectedAt time.Time

	pairCh chan string // signaled with outcome by event handler during phone pair
}

// NewAdapter constructs an Adapter. The container must already be initialized
// (use OpenSQLite or OpenPostgres).
func NewAdapter(container *sqlstore.Container, logger *slog.Logger) *Adapter {
	return &Adapter{container: container, logger: logger}
}

// OpenSQLite opens (or creates) the whatsmeow session DB at path.
func OpenSQLite(ctx context.Context, path string, logger *slog.Logger) (*sqlstore.Container, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	c, err := sqlstore.New(ctx, "sqlite", dsn, waLog.Zerolog(nil))
	if err != nil {
		return nil, fmt.Errorf("sqlstore sqlite: %w", err)
	}
	return c, nil
}

// OpenPostgres opens a Postgres-backed session store using a dedicated schema.
// dsn is the canonical Postgres URL; the schema "whatsmeow_session" is created
// if missing and search_path is scoped to it for this connection.
func OpenPostgres(ctx context.Context, dsn string, logger *slog.Logger) (*sqlstore.Container, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS whatsmeow_session`); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO whatsmeow_session, public`); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	c, err := sqlstore.NewWithDB(ctx, db, "postgres", waLog.Zerolog(nil))
	if err != nil {
		return nil, fmt.Errorf("sqlstore postgres: %w", err)
	}
	return c, nil
}

// ensureClient lazily constructs the whatsmeow.Client backed by the existing
// device (or a fresh one if no session is stored).
func (a *Adapter) ensureClient(ctx context.Context) error {
	if a.client != nil {
		return nil
	}
	device, err := a.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	a.client = whatsmeow.NewClient(device, waLog.Zerolog(nil))
	a.client.AddEventHandler(a.onEvent)
	return nil
}

// onEvent is the single subscription point for whatsmeow events.
func (a *Adapter) onEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		a.mu.Lock()
		a.lastConnectedAt = time.Now()
		a.mu.Unlock()
		a.logger.Info("wa connected")
	case *events.Disconnected:
		a.logger.Info("wa disconnected")
	case *events.LoggedOut:
		a.logger.Info("wa logged out", "reason", evt.Reason.String())
	case *events.PairSuccess:
		a.logger.Info("wa pair success", "jid", evt.ID.String())
		a.signalPair("success")
	case *events.PairError:
		a.logger.Warn("wa pair error", "err", evt.Error.Error())
		a.signalPair("err-" + evt.Error.Error())
	}
}

func (a *Adapter) signalPair(outcome string) {
	a.mu.Lock()
	ch := a.pairCh
	a.pairCh = nil
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- outcome:
		default:
		}
	}
}

// Status returns the current view of the connection.
func (a *Adapter) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil ||
		!a.client.IsConnected() || !a.client.IsLoggedIn() {
		return Status{}
	}
	jid := a.client.Store.ID.String()
	push := a.client.Store.PushName
	since := a.lastConnectedAt
	return Status{
		Connected: true,
		JID:       &jid,
		PushName:  &push,
		Since:     &since,
	}
}

// Resume connects an existing session if one is stored. No-op if none.
func (a *Adapter) Resume(ctx context.Context) error {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.client.Store.ID == nil {
		a.mu.Unlock()
		return nil // no saved session
	}
	a.mu.Unlock()
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// LoginQR starts a fresh QR pairing flow.
func (a *Adapter) LoginQR(ctx context.Context) (<-chan QREvent, error) {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.mu.Unlock()

	qrCh, err := a.client.GetQRChannel(ctx)
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("get qr channel: %w", err)
	}
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}

	out := make(chan QREvent, 4)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.mu.Unlock()
		}()
		for evt := range qrCh {
			switch evt.Event {
			case "code":
				select {
				case out <- QREvent{Code: evt.Code}:
				case <-ctx.Done():
					out <- QREvent{Terminal: true, Outcome: "ctx-cancelled"}
					return
				}
			case "success":
				out <- QREvent{Terminal: true, Outcome: "success"}
				return
			case "timeout":
				out <- QREvent{Terminal: true, Outcome: "timeout"}
				return
			default:
				out <- QREvent{Terminal: true, Outcome: "err-" + evt.Event}
				return
			}
		}
	}()
	return out, nil
}

// LoginPhone starts a phone-pair flow. The first PairEvent carries the 8-char
// pair code; the terminal event arrives when the user enters the code (or the
// 2-minute window expires).
func (a *Adapter) LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error) {
	if !IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}

	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.pairCh = make(chan string, 1)
	pairCh := a.pairCh
	a.mu.Unlock()

	// Connect first so PairPhone can send the linking request.
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}
	code, err := a.client.PairPhone(ctx, phoneNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("pair phone: %w", err)
	}

	out := make(chan PairEvent, 2)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.pairCh = nil
			a.mu.Unlock()
		}()
		out <- PairEvent{Code: code}
		select {
		case outcome := <-pairCh:
			out <- PairEvent{Terminal: true, Outcome: outcome}
		case <-ctx.Done():
			out <- PairEvent{Terminal: true, Outcome: "ctx-cancelled"}
		case <-time.After(150 * time.Second):
			out <- PairEvent{Terminal: true, Outcome: "timeout"}
		}
	}()
	return out, nil
}

// Logout disconnects and tells WhatsApp to invalidate the session.
func (a *Adapter) Logout(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil || !a.client.IsLoggedIn() {
		return ErrNotLoggedIn
	}
	if err := a.client.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Close disconnects without logging out. Called on daemon shutdown.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil && a.client.IsConnected() {
		a.client.Disconnect()
	}
	return nil
}

// compile-time interface check
var _ WAClient = (*Adapter)(nil)
```

> **Note for the implementer:** The whatsmeow API has shifted across versions. If `sqlstore.New` / `sqlstore.NewWithDB` / `client.GetQRChannel` / `client.PairPhone` / `container.GetFirstDevice` signatures don't match what's shown above, run `go doc go.mau.fi/whatsmeow.<symbol>` and adapt. The intent is exact: open the store, lazily build a client, register one event handler, expose `Resume`, `LoginQR` (channel of QR codes ending in a terminal event), `LoginPhone` (channel: code first, terminal second), `Logout`, `Close`.

- [ ] **Step 2: Delete the stub doc.go**

```bash
git rm internal/waclient/doc.go 2>/dev/null || true
# Already removed in Task 2; the rm is here for safety.
```

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output. If you get whatsmeow API mismatches, fix them now per the note above.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```
Expected: PASS. The adapter has no automated tests (covered manually); existing tests must still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: whatsmeow adapter with QR + phone-pair login"
```

---

## Task 4: Service interface and pass-through implementation

**Files:**
- Create: `internal/service/service.go` (replace `internal/service/doc.go`)
- Test: `internal/service/service_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/service/service_test.go`:
```go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWA struct {
	status         waclient.Status
	resumeErr      error
	loginQR        <-chan waclient.QREvent
	loginQRErr     error
	loginPhone     <-chan waclient.PairEvent
	loginPhoneErr  error
	loginPhoneArg  string
	logoutErr      error
	closed         bool
}

func (f *fakeWA) Status() waclient.Status                      { return f.status }
func (f *fakeWA) Resume(context.Context) error                 { return f.resumeErr }
func (f *fakeWA) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return f.loginQR, f.loginQRErr
}
func (f *fakeWA) LoginPhone(_ context.Context, n string) (<-chan waclient.PairEvent, error) {
	f.loginPhoneArg = n
	return f.loginPhone, f.loginPhoneErr
}
func (f *fakeWA) Logout(context.Context) error { return f.logoutErr }
func (f *fakeWA) Close() error                 { f.closed = true; return nil }

func TestStatusPassThrough(t *testing.T) {
	jid := "27821234567@s.whatsapp.net"
	now := time.Now()
	f := &fakeWA{status: waclient.Status{Connected: true, JID: &jid, Since: &now}}
	s := service.New(f)

	got, err := s.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, got.Connected)
	assert.Equal(t, &jid, got.JID)
}

func TestLoginQRPassThrough(t *testing.T) {
	ch := make(chan waclient.QREvent)
	f := &fakeWA{loginQR: ch}
	s := service.New(f)

	got, err := s.LoginQR(context.Background())
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.QREvent)(ch), got)
}

func TestLoginQRError(t *testing.T) {
	f := &fakeWA{loginQRErr: waclient.ErrAlreadyLoggedIn}
	s := service.New(f)
	_, err := s.LoginQR(context.Background())
	assert.ErrorIs(t, err, waclient.ErrAlreadyLoggedIn)
}

func TestLoginPhoneRejectsBadNumber(t *testing.T) {
	f := &fakeWA{}
	s := service.New(f)
	_, err := s.LoginPhone(context.Background(), "27821234567")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phone number")
	assert.Empty(t, f.loginPhoneArg, "fake should not be called")
}

func TestLoginPhonePassThrough(t *testing.T) {
	ch := make(chan waclient.PairEvent)
	f := &fakeWA{loginPhone: ch}
	s := service.New(f)
	got, err := s.LoginPhone(context.Background(), "+27821234567")
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.PairEvent)(ch), got)
	assert.Equal(t, "+27821234567", f.loginPhoneArg)
}

func TestLogoutPassThrough(t *testing.T) {
	f := &fakeWA{logoutErr: errors.New("boom")}
	s := service.New(f)
	err := s.Logout(context.Background())
	assert.ErrorContains(t, err, "boom")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/...`
Expected: FAIL — package missing.

- [ ] **Step 3: Write the implementation**

Remove the stub:
```bash
git rm internal/service/doc.go
```

Create `internal/service/service.go`:
```go
// Package service holds the daemon's use cases. Plan 02 ships pass-through
// methods over WAClient; Plan 04+ will add Store-backed methods.
package service

import (
	"context"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error
}

type svc struct {
	wa waclient.WAClient
}

// New constructs a Service backed by the given WAClient.
func New(wa waclient.WAClient) Service {
	return &svc{wa: wa}
}

func (s *svc) Status(_ context.Context) (waclient.Status, error) {
	return s.wa.Status(), nil
}

func (s *svc) LoginQR(ctx context.Context) (<-chan waclient.QREvent, error) {
	return s.wa.LoginQR(ctx)
}

func (s *svc) LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error) {
	if !waclient.IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}
	return s.wa.LoginPhone(ctx, phoneNumber)
}

func (s *svc) Logout(ctx context.Context) error {
	return s.wa.Logout(ctx)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/service/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: pass-through use-case layer over WAClient"
```

---

## Task 5: Extend Deps with Service field

**Files:**
- Modify: `internal/transport/http/router.go`
- Modify: `internal/transport/http/router_test.go`

- [ ] **Step 1: Add the Service field to Deps**

Edit `internal/transport/http/router.go`. Find the existing `Deps` struct:
```go
type Deps struct {
	Config config.Config
	Logger *slog.Logger
}
```

Replace with:
```go
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	Service service.Service
}
```

Add `"github.com/askarzh/whatsmeow-api/internal/service"` to the import block.

- [ ] **Step 2: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output. (No existing handler uses `Service` yet.)

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```
Expected: PASS — existing tests don't supply `Service` and that's fine because nothing reads it yet.

- [ ] **Step 4: Commit**

```bash
git add internal/transport/http/router.go
git commit -m "http: extend Deps with Service field"
```

---

## Task 6: SSE writer helper

**Files:**
- Create: `internal/transport/http/sse.go`
- Test: `internal/transport/http/sse_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/sse_test.go`:
```go
package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", res.Header.Get("Connection"))
}

func TestSSEWriteEvent(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteEvent(rr, "hello", map[string]string{"k": "v"}))
	got := rr.Body.String()
	// SSE frame: "event: hello\ndata: {\"k\":\"v\"}\n\n"
	assert.True(t, strings.HasPrefix(got, "event: hello\n"), "frame begins with event line, got %q", got)
	assert.Contains(t, got, "data: {\"k\":\"v\"}")
	assert.True(t, strings.HasSuffix(got, "\n\n"), "frame terminates with blank line")
}

func TestSSEWriteHeartbeat(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteHeartbeat(rr))
	assert.Equal(t, ": heartbeat\n\n", rr.Body.String())
}

func TestSSEWriteEventNonObject(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.SSEPrepare(rr)
	require.NoError(t, httpapi.SSEWriteEvent(rr, "x", "scalar"))
	assert.Contains(t, rr.Body.String(), "data: \"scalar\"")
}

// fakeNonFlushable proves SSEWriteEvent is tolerant of writers that don't implement http.Flusher.
type fakeNonFlushable struct{ h http.Header; b []byte }

func (f *fakeNonFlushable) Header() http.Header        { if f.h == nil { f.h = http.Header{} }; return f.h }
func (f *fakeNonFlushable) Write(p []byte) (int, error){ f.b = append(f.b, p...); return len(p), nil }
func (f *fakeNonFlushable) WriteHeader(int)            {}

func TestSSEWriteEventNoFlusher(t *testing.T) {
	w := &fakeNonFlushable{}
	httpapi.SSEPrepare(w)
	require.NoError(t, httpapi.SSEWriteEvent(w, "x", 1))
	assert.Contains(t, string(w.b), "event: x")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transport/http/... -run TestSSE
```
Expected: FAIL — `SSEPrepare`, `SSEWriteEvent`, `SSEWriteHeartbeat` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/sse.go`:
```go
package http

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEPrepare sets the standard SSE response headers. Call once at the start of
// a handler before any SSEWrite* call.
func SSEPrepare(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
}

// SSEWriteEvent writes one Server-Sent Event frame: "event: <name>\ndata: <json>\n\n".
// The payload is encoded as JSON. If the writer implements http.Flusher, the
// frame is flushed immediately so clients see it without buffering.
func SSEWriteEvent(w http.ResponseWriter, name string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, buf); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// SSEWriteHeartbeat writes a comment-only frame, used to keep proxies from
// closing idle SSE connections.
func SSEWriteHeartbeat(w http.ResponseWriter) error {
	if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/transport/http/... -run TestSSE -v
```
Expected: all 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/sse.go internal/transport/http/sse_test.go
git commit -m "http: SSE writer helpers (event, heartbeat, prepare)"
```

---

## Task 7: Rewrite /v1/status to use Service

**Files:**
- Modify: `internal/transport/http/status.go`
- Modify: `internal/transport/http/status_test.go`

- [ ] **Step 1: Replace the test**

Overwrite `internal/transport/http/status_test.go`:
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
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStatusSvc struct{ st waclient.Status }

func (f fakeStatusSvc) Status(context.Context) (waclient.Status, error)                         { return f.st, nil }
func (f fakeStatusSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)                { return nil, nil }
func (f fakeStatusSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error)   { return nil, nil }
func (f fakeStatusSvc) Logout(context.Context) error                                            { return nil }

var _ service.Service = fakeStatusSvc{}

func TestStatusHandlerDisconnected(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	httpapi.StatusHandler(fakeStatusSvc{st: waclient.Status{}}).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, false, body["wa_connected"])
	assert.Nil(t, body["jid"])
	assert.Nil(t, body["push_name"])
	assert.Nil(t, body["since"])
}

func TestStatusHandlerConnected(t *testing.T) {
	jid := "27821234567@s.whatsapp.net"
	push := "Askar"
	since := time.Date(2026, 4, 30, 11, 23, 45, 0, time.UTC)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	httpapi.StatusHandler(fakeStatusSvc{st: waclient.Status{
		Connected: true, JID: &jid, PushName: &push, Since: &since,
	}}).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, true, body["wa_connected"])
	assert.Equal(t, jid, body["jid"])
	assert.Equal(t, push, body["push_name"])
	assert.Equal(t, "2026-04-30T11:23:45Z", body["since"])
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transport/http/... -run TestStatusHandler
```
Expected: FAIL — `StatusHandler` now expects a `Service` argument.

- [ ] **Step 3: Replace the implementation**

Overwrite `internal/transport/http/status.go`:
```go
package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// StatusHandler reports the WhatsApp connection state from the service layer.
func StatusHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.Status(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "wa.internal", err.Error())
			return
		}
		body := map[string]any{
			"wa_connected": st.Connected,
			"jid":          nilOrString(st.JID),
			"push_name":    nilOrString(st.PushName),
			"since":        nilOrTime(st.Since),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}

func nilOrString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nilOrTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/transport/http/... -run TestStatusHandler -v
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/status.go internal/transport/http/status_test.go
git commit -m "http: /v1/status returns real connection state from Service"
```

---

## Task 8: /v1/login/qr SSE handler

**Files:**
- Create: `internal/transport/http/login_qr.go`
- Test: `internal/transport/http/login_qr_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/login_qr_test.go`:
```go
package http_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLoginQRSvc struct {
	ch  <-chan waclient.QREvent
	err error
}

func (f fakeLoginQRSvc) Status(context.Context) (waclient.Status, error)                { return waclient.Status{}, nil }
func (f fakeLoginQRSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)       { return f.ch, f.err }
func (f fakeLoginQRSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f fakeLoginQRSvc) Logout(context.Context) error                                   { return nil }

var _ service.Service = fakeLoginQRSvc{}

func TestLoginQRStreamsCodesThenSuccess(t *testing.T) {
	ch := make(chan waclient.QREvent, 3)
	ch <- waclient.QREvent{Code: "2@first"}
	ch <- waclient.QREvent{Code: "2@second"}
	ch <- waclient.QREvent{Terminal: true, Outcome: "success"}
	close(ch)

	srv := httptest.NewServer(httpapi.LoginQRHandler(fakeLoginQRSvc{ch: ch}))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

	frames := readSSEFrames(t, res)
	require.GreaterOrEqual(t, len(frames), 3)
	assert.Equal(t, "qr", frames[0].event)
	assert.Contains(t, frames[0].data, `"code":"2@first"`)
	assert.Equal(t, "qr", frames[1].event)
	assert.Contains(t, frames[1].data, `"code":"2@second"`)
	assert.Equal(t, "connection", frames[2].event)
	assert.Contains(t, frames[2].data, `"outcome":"success"`)
}

func TestLoginQRConflict(t *testing.T) {
	srv := httptest.NewServer(httpapi.LoginQRHandler(fakeLoginQRSvc{err: waclient.ErrAlreadyLoggedIn}))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
}

type sseFrame struct{ event, data string }

func readSSEFrames(t *testing.T, res *http.Response) []sseFrame {
	t.Helper()
	var out []sseFrame
	cur := sseFrame{}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" || cur.data != "" {
				out = append(out, cur)
				cur = sseFrame{}
			}
		}
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transport/http/... -run TestLoginQR
```
Expected: FAIL — `LoginQRHandler` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/login_qr.go`:
```go
package http

import (
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// LoginQRHandler streams whatsmeow QR codes as SSE events.
func LoginQRHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch, err := svc.LoginQR(r.Context())
		if err != nil {
			switch {
			case errors.Is(err, waclient.ErrAlreadyLoggedIn):
				WriteProblem(w, http.StatusConflict, "wa.already_logged_in", err.Error())
			case errors.Is(err, waclient.ErrLoginInProgress):
				WriteProblem(w, http.StatusConflict, "wa.login_in_progress", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.login_failed", err.Error())
			}
			return
		}

		SSEPrepare(w)
		w.WriteHeader(http.StatusOK)

		for evt := range ch {
			if evt.Terminal {
				_ = SSEWriteEvent(w, "connection", map[string]any{"outcome": evt.Outcome})
				return
			}
			_ = SSEWriteEvent(w, "qr", map[string]any{"code": evt.Code, "expires_in_s": 20})
		}
	})
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/transport/http/... -run TestLoginQR -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/login_qr.go internal/transport/http/login_qr_test.go
git commit -m "http: /v1/login/qr SSE handler"
```

---

## Task 9: /v1/login/phone SSE handler

**Files:**
- Create: `internal/transport/http/login_phone.go`
- Test: `internal/transport/http/login_phone_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/login_phone_test.go`:
```go
package http_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLoginPhoneSvc struct {
	ch     <-chan waclient.PairEvent
	err    error
	gotNum string
}

func (f *fakeLoginPhoneSvc) Status(context.Context) (waclient.Status, error)                       { return waclient.Status{}, nil }
func (f *fakeLoginPhoneSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f *fakeLoginPhoneSvc) LoginPhone(_ context.Context, n string) (<-chan waclient.PairEvent, error) {
	f.gotNum = n
	return f.ch, f.err
}
func (f *fakeLoginPhoneSvc) Logout(context.Context) error { return nil }

var _ service.Service = (*fakeLoginPhoneSvc)(nil)

func TestLoginPhoneStreamsCodeThenSuccess(t *testing.T) {
	ch := make(chan waclient.PairEvent, 2)
	ch <- waclient.PairEvent{Code: "ABCD-1234"}
	ch <- waclient.PairEvent{Terminal: true, Outcome: "success"}
	close(ch)

	f := &fakeLoginPhoneSvc{ch: ch}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := strings.NewReader(`{"phone_number":"+27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	assert.Equal(t, "+27821234567", f.gotNum)

	frames := readSSEFrames(t, res)
	require.GreaterOrEqual(t, len(frames), 2)
	assert.Equal(t, "pair_code", frames[0].event)
	assert.Contains(t, frames[0].data, `"code":"ABCD-1234"`)
	assert.Equal(t, "connection", frames[1].event)
	assert.Contains(t, frames[1].data, `"outcome":"success"`)
}

func TestLoginPhoneRejectsBadNumber(t *testing.T) {
	f := &fakeLoginPhoneSvc{}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"phone_number":"27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
	assert.Empty(t, f.gotNum)
}

func TestLoginPhoneRejectsMalformedJSON(t *testing.T) {
	f := &fakeLoginPhoneSvc{}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`not json`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLoginPhoneConflict(t *testing.T) {
	f := &fakeLoginPhoneSvc{err: waclient.ErrLoginInProgress}
	srv := httptest.NewServer(httpapi.LoginPhoneHandler(f))
	defer srv.Close()

	body := strings.NewReader(`{"phone_number":"+27821234567"}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transport/http/... -run TestLoginPhone
```
Expected: FAIL — `LoginPhoneHandler` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/login_phone.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type loginPhoneRequest struct {
	PhoneNumber string `json:"phone_number"`
}

// LoginPhoneHandler streams the phone-pair flow as SSE: first event is the
// pairing code, the terminal event is the connection outcome.
func LoginPhoneHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req loginPhoneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		if !waclient.IsValidPhoneNumber(req.PhoneNumber) {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "phone_number must be E.164 (e.g. +27821234567)")
			return
		}

		ch, err := svc.LoginPhone(r.Context(), req.PhoneNumber)
		if err != nil {
			switch {
			case errors.Is(err, waclient.ErrAlreadyLoggedIn):
				WriteProblem(w, http.StatusConflict, "wa.already_logged_in", err.Error())
			case errors.Is(err, waclient.ErrLoginInProgress):
				WriteProblem(w, http.StatusConflict, "wa.login_in_progress", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.login_failed", err.Error())
			}
			return
		}

		SSEPrepare(w)
		w.WriteHeader(http.StatusOK)

		for evt := range ch {
			if evt.Terminal {
				_ = SSEWriteEvent(w, "connection", map[string]any{"outcome": evt.Outcome})
				return
			}
			_ = SSEWriteEvent(w, "pair_code", map[string]any{"code": evt.Code, "expires_in_s": 120})
		}
	})
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/transport/http/... -run TestLoginPhone -v
```
Expected: all 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/login_phone.go internal/transport/http/login_phone_test.go
git commit -m "http: /v1/login/phone SSE handler with E.164 validation"
```

---

## Task 10: /v1/logout handler

**Files:**
- Create: `internal/transport/http/logout.go`
- Test: `internal/transport/http/logout_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/logout_test.go`:
```go
package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/service"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
)

type fakeLogoutSvc struct{ err error }

func (f fakeLogoutSvc) Status(context.Context) (waclient.Status, error)                       { return waclient.Status{}, nil }
func (f fakeLogoutSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f fakeLogoutSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f fakeLogoutSvc) Logout(context.Context) error                                          { return f.err }

var _ service.Service = fakeLogoutSvc{}

func TestLogoutSuccess(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logout", nil)
	httpapi.LogoutHandler(fakeLogoutSvc{}).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestLogoutNotLoggedIn(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logout", nil)
	httpapi.LogoutHandler(fakeLogoutSvc{err: waclient.ErrNotLoggedIn}).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transport/http/... -run TestLogout
```
Expected: FAIL — `LogoutHandler` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/logout.go`:
```go
package http

import (
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// LogoutHandler tells WhatsApp to invalidate the current session and disconnects.
func LogoutHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := svc.Logout(r.Context())
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, waclient.ErrNotLoggedIn):
			WriteProblem(w, http.StatusConflict, "wa.not_logged_in", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.internal", err.Error())
		}
	})
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/transport/http/... -run TestLogout -v
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/logout.go internal/transport/http/logout_test.go
git commit -m "http: /v1/logout handler"
```

---

## Task 11: Wire endpoints into the router

**Files:**
- Modify: `internal/transport/http/router.go`
- Modify: `internal/transport/http/router_test.go`

- [ ] **Step 1: Update the router**

Edit `internal/transport/http/router.go`. Find the `/v1` block:
```go
	r.Route("/v1", func(r chi.Router) {
		// public
		r.Method(http.MethodGet, "/health", HealthHandler())

		// protected
		r.Group(func(r chi.Router) {
			r.Use(RequireBearerToken(d.Config.Auth.Token))
			r.Method(http.MethodGet, "/status", StatusHandler())
		})
	})
```

Replace it with:
```go
	r.Route("/v1", func(r chi.Router) {
		// public
		r.Method(http.MethodGet, "/health", HealthHandler())

		// protected
		r.Group(func(r chi.Router) {
			r.Use(RequireBearerToken(d.Config.Auth.Token))
			r.Method(http.MethodGet, "/status", StatusHandler(d.Service))
			r.Method(http.MethodPost, "/login/qr", LoginQRHandler(d.Service))
			r.Method(http.MethodPost, "/login/phone", LoginPhoneHandler(d.Service))
			r.Method(http.MethodPost, "/logout", LogoutHandler(d.Service))
		})
	})
```

- [ ] **Step 2: Update the existing router tests**

Edit `internal/transport/http/router_test.go`. The existing tests construct `httpapi.Deps{Config: config.Config{...}}` without a `Service`. The new `StatusHandler(svc)` will panic if `Service` is nil. Add a no-op fake.

At the top of the file, add the imports if missing:
```go
	"context"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
```

Add this fake before the existing tests:
```go
type routerFakeSvc struct{}

func (routerFakeSvc) Status(context.Context) (waclient.Status, error)                        { return waclient.Status{}, nil }
func (routerFakeSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)               { return nil, nil }
func (routerFakeSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error)  { return nil, nil }
func (routerFakeSvc) Logout(context.Context) error                                           { return nil }

var _ service.Service = routerFakeSvc{}
```

Then change every `httpapi.Deps{Config: config.Config{...}}` literal in this file to also include `Service: routerFakeSvc{}`. There are three calls: in `TestRouterHealthIsPublic`, `TestRouterStatusRequiresAuth`, `TestRouterAuthDisabledStatusOpen`.

For example:
```go
r := httpapi.NewRouter(httpapi.Deps{
    Config:  config.Config{Auth: config.AuthConfig{Token: "s3cret"}},
    Service: routerFakeSvc{},
})
```

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/transport/http/... -v
```
Expected: all PASS, including the four new endpoint tests already added in Tasks 7-10 plus the three router tests now using the fake service.

- [ ] **Step 4: Commit**

```bash
git add internal/transport/http/router.go internal/transport/http/router_test.go
git commit -m "http: wire login/logout/status routes through Service"
```

---

## Task 12: internal/client — HTTP+SSE client of the daemon

**Files:**
- Create: `internal/client/client.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/client/client_test.go`:
```go
package client_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/status", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer t", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"wa_connected":true,"jid":"j@s.whatsapp.net","push_name":"a","since":"2026-04-30T11:23:45Z"}`)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "t")
	st, err := c.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, st.WAConnected)
	assert.Equal(t, "j@s.whatsapp.net", st.JID)
	assert.Equal(t, "a", st.PushName)
}

func TestLogoutHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/logout", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	err := c.Logout(context.Background())
	assert.NoError(t, err)
}

func TestLogoutNotLoggedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"code":"wa.not_logged_in","detail":"x"}`)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	err := c.Logout(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, client.ErrNotLoggedIn)
}

func TestLoginQRStream(t *testing.T) {
	body := "event: qr\ndata: {\"code\":\"2@a\"}\n\nevent: qr\ndata: {\"code\":\"2@b\"}\n\nevent: connection\ndata: {\"outcome\":\"success\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	ch, err := c.LoginQR(context.Background())
	require.NoError(t, err)

	var got []client.QREvent
	for ev := range ch {
		got = append(got, ev)
	}
	require.Len(t, got, 3)
	assert.Equal(t, "2@a", got[0].Code)
	assert.Equal(t, "2@b", got[1].Code)
	assert.True(t, got[2].Terminal)
	assert.Equal(t, "success", got[2].Outcome)
}

func TestLoginPhoneStream(t *testing.T) {
	body := "event: pair_code\ndata: {\"code\":\"ABCD-1234\"}\n\nevent: connection\ndata: {\"outcome\":\"success\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/login/phone", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	ch, err := c.LoginPhone(context.Background(), "+27821234567")
	require.NoError(t, err)

	var got []client.PairEvent
	for ev := range ch {
		got = append(got, ev)
	}
	require.Len(t, got, 2)
	assert.Equal(t, "ABCD-1234", got[0].Code)
	assert.True(t, got[1].Terminal)
	assert.Equal(t, "success", got[1].Outcome)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/client/...
```
Expected: FAIL — package missing.

- [ ] **Step 3: Write the implementation**

Create `internal/client/client.go`:
```go
// Package client wraps the daemon's HTTP+SSE API for use by the CLI and other
// in-process consumers.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client talks to a running whatsmeow-api daemon.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// New constructs a Client. baseURL is e.g. "http://127.0.0.1:8080". token is
// the bearer token; pass "" if the daemon has auth disabled.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 0}, // no timeout: SSE streams are long-lived
	}
}

// Status mirrors the /v1/status response.
type Status struct {
	WAConnected bool   `json:"wa_connected"`
	JID         string `json:"jid"`
	PushName    string `json:"push_name"`
	Since       string `json:"since"`
}

// QREvent matches the daemon's SSE qr/connection events.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// PairEvent matches the daemon's SSE pair_code/connection events.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// Sentinel errors so callers can branch.
var (
	ErrNotLoggedIn     = errors.New("client: daemon reports not logged in")
	ErrAlreadyLoggedIn = errors.New("client: daemon reports already logged in")
	ErrLoginInProgress = errors.New("client: daemon reports login in progress")
)

func (c *Client) addAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// Status fetches the current connection state.
func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/status", nil)
	if err != nil {
		return Status{}, err
	}
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Status{}, problemError(res)
	}
	var st Status
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return Status{}, fmt.Errorf("decode status: %w", err)
	}
	return st, nil
}

// Logout requests the daemon to log out.
func (c *Client) Logout(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/logout", nil)
	if err != nil {
		return err
	}
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return nil
	}
	return problemError(res)
}

// LoginQR opens an SSE stream and returns a channel emitting QR events.
// The channel is closed after the terminal event.
func (c *Client) LoginQR(ctx context.Context) (<-chan QREvent, error) {
	res, err := c.openSSE(ctx, "/v1/login/qr", nil)
	if err != nil {
		return nil, err
	}
	out := make(chan QREvent)
	go func() {
		defer close(out)
		defer res.Body.Close()
		for f := range readSSE(ctx, res.Body) {
			switch f.event {
			case "qr":
				var p struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- QREvent{Code: p.Code}:
				case <-ctx.Done():
					return
				}
			case "connection":
				var p struct {
					Outcome string `json:"outcome"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- QREvent{Terminal: true, Outcome: p.Outcome}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return out, nil
}

// LoginPhone opens an SSE stream for phone-pair login.
func (c *Client) LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error) {
	body := bytes.NewBufferString(fmt.Sprintf(`{"phone_number":%q}`, phoneNumber))
	res, err := c.openSSE(ctx, "/v1/login/phone", body)
	if err != nil {
		return nil, err
	}
	out := make(chan PairEvent)
	go func() {
		defer close(out)
		defer res.Body.Close()
		for f := range readSSE(ctx, res.Body) {
			switch f.event {
			case "pair_code":
				var p struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- PairEvent{Code: p.Code}:
				case <-ctx.Done():
					return
				}
			case "connection":
				var p struct {
					Outcome string `json:"outcome"`
				}
				_ = json.Unmarshal([]byte(f.data), &p)
				select {
				case out <- PairEvent{Terminal: true, Outcome: p.Outcome}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return out, nil
}

func (c *Client) openSSE(ctx context.Context, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")
	c.addAuth(req)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		err := problemError(res)
		res.Body.Close()
		return nil, err
	}
	return res, nil
}

type sseFrame struct{ event, data string }

func readSSE(ctx context.Context, r io.Reader) <-chan sseFrame {
	out := make(chan sseFrame)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.event != "" || cur.data != "" {
					select {
					case out <- cur:
					case <-ctx.Done():
						return
					}
					cur = sseFrame{}
				}
			}
		}
	}()
	return out
}

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func problemError(res *http.Response) error {
	defer io.Copy(io.Discard, res.Body) //nolint:errcheck
	var p problem
	_ = json.NewDecoder(res.Body).Decode(&p)
	switch p.Code {
	case "wa.not_logged_in":
		return ErrNotLoggedIn
	case "wa.already_logged_in":
		return ErrAlreadyLoggedIn
	case "wa.login_in_progress":
		return ErrLoginInProgress
	}
	return fmt.Errorf("daemon returned %d (%s): %s", res.StatusCode, p.Code, p.Detail)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/client/... -v
```
Expected: all 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/client.go internal/client/client_test.go
git commit -m "client: HTTP+SSE client of the daemon API"
```

---

## Task 13: CLI `status` subcommand

**Files:**
- Create: `cmd/whatsmeow-api/status.go`
- Modify: `cmd/whatsmeow-api/main.go` (register the subcommand)

- [ ] **Step 1: Create the subcommand**

Create `cmd/whatsmeow-api/status.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the daemon's WhatsApp connection state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newDaemonClient(cmd)
			st, err := c.Status(context.Background())
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			if !st.WAConnected {
				fmt.Println("not connected")
				return nil
			}
			fmt.Printf("connected as %s (%s) since %s\n", st.JID, st.PushName, st.Since)
			return nil
		},
	}
	return cmd
}

// newDaemonClient resolves the daemon URL and bearer token from --url/--token
// flags, falling back to WMAPI_URL/WMAPI_TOKEN env vars and finally to
// http://127.0.0.1:8080 with no token.
func newDaemonClient(cmd *cobra.Command) *client.Client {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		url = os.Getenv("WMAPI_URL")
	}
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	token, _ := cmd.Flags().GetString("token")
	if token == "" {
		token = os.Getenv("WMAPI_TOKEN")
	}
	return client.New(url, token)
}
```

- [ ] **Step 2: Register the subcommand and add the persistent client flags**

Edit `cmd/whatsmeow-api/main.go`. The current file looks like:
```go
func main() {
	root := &cobra.Command{
		Use:           "whatsmeow-api",
		Short:         "HTTP/SSE API daemon wrapping whatsmeow",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to config TOML (optional)")
	root.AddCommand(serveCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

Modify it to:
```go
func main() {
	root := &cobra.Command{
		Use:           "whatsmeow-api",
		Short:         "HTTP/SSE API daemon wrapping whatsmeow",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to config TOML (optional)")
	root.PersistentFlags().String("url", "", "daemon URL (default $WMAPI_URL or http://127.0.0.1:8080)")
	root.PersistentFlags().String("token", "", "daemon bearer token (default $WMAPI_TOKEN)")
	root.AddCommand(serveCmd())
	root.AddCommand(statusCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output.

- [ ] **Step 4: Smoke test against a running daemon**

Start the daemon in one terminal: `./bin/whatsmeow-api serve`. In another:
```bash
go run ./cmd/whatsmeow-api status
```
Expected output: `not connected`. (Plan 02's serve hasn't been wired with waclient yet, so the daemon will still respond with the placeholder. After Task 17 the response will become the real one.)

If you skip this manual check (because the binary isn't running), at minimum run `go build ./cmd/whatsmeow-api/...` and confirm it compiles.

- [ ] **Step 5: Commit**

```bash
git add cmd/whatsmeow-api/status.go cmd/whatsmeow-api/main.go
git commit -m "cmd: status subcommand (CLI client of the daemon)"
```

---

## Task 14: CLI `logout` subcommand

**Files:**
- Create: `cmd/whatsmeow-api/logout.go`
- Modify: `cmd/whatsmeow-api/main.go`

- [ ] **Step 1: Create the subcommand**

Create `cmd/whatsmeow-api/logout.go`:
```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/spf13/cobra"
)

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log the daemon's WhatsApp session out",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newDaemonClient(cmd)
			err := c.Logout(context.Background())
			switch {
			case err == nil:
				fmt.Println("logged out")
				return nil
			case errors.Is(err, client.ErrNotLoggedIn):
				fmt.Println("not logged in")
				return nil
			default:
				return fmt.Errorf("logout: %w", err)
			}
		},
	}
}
```

- [ ] **Step 2: Register the subcommand**

Edit `cmd/whatsmeow-api/main.go`. After the `root.AddCommand(statusCmd())` line, add:
```go
	root.AddCommand(logoutCmd())
```

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add cmd/whatsmeow-api/logout.go cmd/whatsmeow-api/main.go
git commit -m "cmd: logout subcommand"
```

---

## Task 15: CLI `login phone <number>` subcommand

**Files:**
- Create: `cmd/whatsmeow-api/login.go`
- Modify: `cmd/whatsmeow-api/main.go`

We add the parent `login` command + the `phone` subcommand here. The `qr` subcommand follows in Task 16.

- [ ] **Step 1: Create the subcommand**

Create `cmd/whatsmeow-api/login.go`:
```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/askarzh/whatsmeow-api/internal/client"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/spf13/cobra"
)

func loginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Pair the daemon with a WhatsApp account",
	}
	cmd.AddCommand(loginPhoneCmd())
	return cmd
}

func loginPhoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "phone <number>",
		Short: "Pair via phone number (8-character link code)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := args[0]
			if !waclient.IsValidPhoneNumber(number) {
				return fmt.Errorf("invalid phone number %q (must be E.164, e.g. +27821234567)", number)
			}

			c := newDaemonClient(cmd)
			ch, err := c.LoginPhone(context.Background(), number)
			if err != nil {
				switch {
				case errors.Is(err, client.ErrAlreadyLoggedIn):
					fmt.Fprintln(os.Stderr, "already logged in")
					os.Exit(1)
				case errors.Is(err, client.ErrLoginInProgress):
					fmt.Fprintln(os.Stderr, "another login is already in progress")
					os.Exit(1)
				}
				return fmt.Errorf("login phone: %w", err)
			}

			for evt := range ch {
				if evt.Terminal {
					if evt.Outcome == "success" {
						fmt.Println("logged in")
						return nil
					}
					return fmt.Errorf("pairing failed: %s", evt.Outcome)
				}
				fmt.Printf("Pair code: %s\n  Open WhatsApp → Settings → Linked Devices → Link with phone number → enter the code (expires in ~2 min)\n", evt.Code)
			}
			return fmt.Errorf("pairing stream closed without terminal event")
		},
	}
}
```

- [ ] **Step 2: Register the subcommand**

Edit `cmd/whatsmeow-api/main.go`. After `root.AddCommand(logoutCmd())`, add:
```go
	root.AddCommand(loginCmd())
```

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add cmd/whatsmeow-api/login.go cmd/whatsmeow-api/main.go
git commit -m "cmd: login phone subcommand (E.164 input, prints code)"
```

---

## Task 16: CLI `login qr` subcommand

**Files:**
- Modify: `cmd/whatsmeow-api/login.go` (add `qr` subcommand)

- [ ] **Step 1: Add the subcommand function**

Append to `cmd/whatsmeow-api/login.go` (anywhere after the existing functions):
```go
func loginQRCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "qr",
		Short: "Pair by scanning a QR code with your phone",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newDaemonClient(cmd)
			ch, err := c.LoginQR(context.Background())
			if err != nil {
				switch {
				case errors.Is(err, client.ErrAlreadyLoggedIn):
					fmt.Fprintln(os.Stderr, "already logged in")
					os.Exit(1)
				case errors.Is(err, client.ErrLoginInProgress):
					fmt.Fprintln(os.Stderr, "another login is already in progress")
					os.Exit(1)
				}
				return fmt.Errorf("login qr: %w", err)
			}

			for evt := range ch {
				if evt.Terminal {
					if evt.Outcome == "success" {
						fmt.Println("\nlogged in")
						return nil
					}
					return fmt.Errorf("pairing failed: %s", evt.Outcome)
				}
				renderQR(evt.Code)
			}
			return fmt.Errorf("pairing stream closed without terminal event")
		},
	}
}

func renderQR(code string) {
	// Clear the screen so each QR is rendered cleanly.
	fmt.Print("\033[H\033[2J")
	fmt.Println("Scan this with WhatsApp → Settings → Linked Devices → Link a Device:")
	qrterminal.Generate(code, qrterminal.M, os.Stdout)
	fmt.Println("(expires in ~20s, will refresh)")
}
```

Add the new import `"github.com/mdp/qrterminal/v3"` to the existing import block at the top of the file.

- [ ] **Step 2: Wire it into the parent `login` command**

Inside the existing `loginCmd()` function, add the line:
```go
	cmd.AddCommand(loginQRCmd())
```
right after `cmd.AddCommand(loginPhoneCmd())`.

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add cmd/whatsmeow-api/login.go
git commit -m "cmd: login qr subcommand (renders QR with qrterminal)"
```

---

## Task 17: Wire serve to construct waclient + service + auto-resume

**Files:**
- Modify: `cmd/whatsmeow-api/serve.go`

- [ ] **Step 1: Replace the body of serveCmd**

Overwrite `cmd/whatsmeow-api/serve.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/askarzh/whatsmeow-api/internal/logging"
	"github.com/askarzh/whatsmeow-api/internal/service"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("validate config: %w", err)
			}

			logger, err := logging.New(cfg.Log, os.Stdout)
			if err != nil {
				return fmt.Errorf("init logger: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Ensure data_dir exists.
			if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
				return fmt.Errorf("create data_dir %q: %w", cfg.DataDir, err)
			}

			// Open whatsmeow's session store per the configured backend.
			var (
				container interface {
					Close() error
				}
				wa *waclient.Adapter
			)
			switch cfg.Storage.Backend {
			case "sqlite":
				path := filepath.Join(cfg.DataDir, "whatsmeow-session.db")
				c, err := waclient.OpenSQLite(ctx, path, logger)
				if err != nil {
					return fmt.Errorf("open sqlite session store: %w", err)
				}
				container = c
				wa = waclient.NewAdapter(c, logger)
			case "postgres":
				c, err := waclient.OpenPostgres(ctx, cfg.Storage.PostgresDSN, logger)
				if err != nil {
					return fmt.Errorf("open postgres session store: %w", err)
				}
				container = c
				wa = waclient.NewAdapter(c, logger)
			default:
				return fmt.Errorf("unsupported storage backend %q", cfg.Storage.Backend)
			}
			defer func() {
				_ = wa.Close()
				_ = container.Close()
			}()

			if err := wa.Resume(ctx); err != nil {
				logger.Warn("session resume failed; awaiting /v1/login/*", "err", err)
			}

			svc := service.New(wa)

			srv := httpapi.NewServer(httpapi.Deps{
				Config:  cfg,
				Logger:  logger,
				Service: svc,
			})

			logger.Info("server starting", "bind", cfg.Server.Bind, "port", cfg.Server.Port)
			if err := srv.Run(ctx); err != nil {
				return fmt.Errorf("server: %w", err)
			}
			logger.Info("server stopped")
			return nil
		},
	}
}
```

> Note: the `container` variable is declared as an interface with only `Close()` because `*sqlstore.Container`'s exact type-switching shouldn't leak into serve.go. If `*sqlstore.Container` doesn't have a `Close()` method in the version installed, drop the deferred close (the GC handles the underlying *sql.DB) and let the variable go.

- [ ] **Step 2: Build and vet**

```bash
go build ./...
go vet ./...
```
Expected: no output. If `Close()` doesn't exist on the container type, simplify: keep just the `wa` variable and skip the container interface.

- [ ] **Step 3: Run the test suite**

```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/whatsmeow-api/serve.go
git commit -m "cmd: serve constructs waclient + service + auto-resumes session"
```

---

## Task 18: End-to-end smoke test

**Files:** none modified.

This task verifies the daemon boots, attempts resume cleanly when no session exists, and the CLI happily talks to it. It does **not** require a real WhatsApp account or pairing.

- [ ] **Step 1: Build the binary**

```bash
make build
ls -la bin/whatsmeow-api
```

- [ ] **Step 2: Start the daemon (foreground or background)**

Background variant:
```bash
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 1
cat /tmp/wmapi.log
```

Expected log lines (in order, give or take timing):
- `... msg="server starting" bind=127.0.0.1 port=8080`
- The `Resume` call may log a warn or info; either is fine as long as the daemon stays up.

- [ ] **Step 3: Verify `/v1/health`**

```bash
curl -s http://127.0.0.1:8080/v1/health
```
Expected: `{"db":null,"ok":true,"wa_connected":null}`.

- [ ] **Step 4: Verify `/v1/status` (now real, not the placeholder)**

```bash
curl -s http://127.0.0.1:8080/v1/status
```
Expected: `{"jid":null,"push_name":null,"since":null,"wa_connected":false}` (key order may vary).

- [ ] **Step 5: Verify CLI `status`**

```bash
./bin/whatsmeow-api status
```
Expected: `not connected`.

- [ ] **Step 6: Verify CLI `logout` when not logged in**

```bash
./bin/whatsmeow-api logout
```
Expected: `not logged in` (exit 0).

- [ ] **Step 7: Verify the `login phone` flow rejects bad numbers**

```bash
./bin/whatsmeow-api login phone 27821234567
```
Expected: `error: invalid phone number "27821234567" (must be E.164, e.g. +27821234567)` to stderr, exit 1.

- [ ] **Step 8: Verify the `login qr` SSE reaches the daemon and starts streaming**

`login qr` will actually attempt to pair against WhatsApp servers. Without a real account scan it will time out after roughly 2 minutes. To avoid that, we just confirm the SSE upgrade works by curl:

```bash
curl -sN -X POST http://127.0.0.1:8080/v1/login/qr | head -5 &
PID=$!
sleep 3
kill $PID 2>/dev/null
```
Expected within 3 seconds: at least one `event: qr` line followed by `data: {"code":"2@...","expires_in_s":20}`. If the line never appears, either the daemon couldn't reach WhatsApp's servers (offline) or the adapter is broken — investigate.

- [ ] **Step 9: Stop the daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -5 /tmp/wmapi.log
```
Expected last line: `... msg="server stopped"`.

- [ ] **Step 10: Mark this task done**

No commit — code is unchanged.

If any step failed, fix the underlying code in the appropriate package (or escalate) before proceeding.

---

## Task 19: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace the body**

Overwrite `README.md`:
```markdown
# whatsmeow-api

A long-running HTTP/SSE daemon that wraps [`whatsmeow`](https://github.com/tulir/whatsmeow) and exposes a stable JSON API for a single WhatsApp account. Designed primarily as a backend for an MCP server, but usable from any HTTP client.

## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.

App-level chat/message storage and messaging endpoints land in Plan 03 / Plan 04.

## Quick start

```bash
make build
./bin/whatsmeow-api serve

# in another terminal:
./bin/whatsmeow-api login qr        # scan with WhatsApp on your phone
./bin/whatsmeow-api status          # confirm pairing
```

`login phone +27821234567` is the alternative if you can't scan a QR — the daemon prints an 8-character code; enter it on the linked-device screen.

## Configuration

Source order (highest precedence first):

1. `WMAPI_*` environment variables (use `WMAPI_SECTION__KEY` for nested keys, e.g. `WMAPI_SERVER__PORT=9000`)
2. `--config /path/to/config.toml`
3. Built-in defaults

The CLI subcommands (`status`, `logout`, `login`) talk to a running daemon over HTTP. Resolution: `--url`/`--token` flags > `WMAPI_URL`/`WMAPI_TOKEN` env vars > `http://127.0.0.1:8080` with no token.

See `config.example.toml` for the full key list.

### Auth fail-safe

If `auth.token` is empty the daemon refuses to start unless it binds to a loopback address (e.g. `127.0.0.1` or `::1`). Set `auth.token` whenever you bind elsewhere.

## Layout

See `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md` for the master design and the `02-waclient-design.md` spec for Plan 02 details.

## License

TBD
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 02"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` all PASS
- [ ] Manual smoke from Task 18 all green
- [ ] (Optional, for the brave) actual pairing with a real WhatsApp account succeeds via `./bin/whatsmeow-api login qr`
- [ ] `git log --oneline` shows ~16 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 03 — app store + SQLite**.
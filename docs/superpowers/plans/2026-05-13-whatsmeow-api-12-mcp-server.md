# whatsmeow-api Plan 12 — MCP Server Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an MCP-over-streamable-HTTP transport at `POST/GET /v1/mcp` so Claude (Code, desktop, claude.ai) can drive the WhatsApp account through 25 structured tools backed directly by `service.Service`.

**Architecture:** New `internal/transport/mcp` package, mounted by `httpapi.NewRouter` under the existing auth-protected `/v1` group. The official `github.com/modelcontextprotocol/go-sdk` SDK provides the server, in-memory transport (tests), and streamable-HTTP handler (production). Tool handlers call `service.Service` directly — no double-hop through REST. Errors map via a small wrapper: `service.ErrInvalidRequest` / `service.ErrForbidden` / `store.ErrNotFound` → `CallToolResult{IsError: true}`; anything else → JSON-RPC internal error.

**Tech Stack:**
- Go 1.26.2 (already in `go.mod`).
- `github.com/modelcontextprotocol/go-sdk` — pinned during Task 1.
- `github.com/go-chi/chi/v5` — existing.
- `github.com/stretchr/testify/require` + `assert` — existing.

---

## File Structure

| Path | Action | Notes |
|---|---|---|
| `go.mod`, `go.sum` | MODIFY | Add `github.com/modelcontextprotocol/go-sdk` dependency |
| `internal/config/config.go` | MODIFY | Add `MCPConfig{Enabled bool}` and default `mcp.enabled=true` |
| `internal/config/config_test.go` | MODIFY | Assert the new default + env override |
| `internal/transport/mcp/server.go` | NEW | `New(deps) http.Handler` — entry point; constructs `mcp.Server`, registers tools, returns streamable-HTTP handler |
| `internal/transport/mcp/errors.go` | NEW | `wrap(err) *mcp.CallToolResult` — single mapping function |
| `internal/transport/mcp/errors_test.go` | NEW | Unit-test the four error branches |
| `internal/transport/mcp/tools_status.go` | NEW | `wa_status`, `wa_stats` |
| `internal/transport/mcp/tools_chats.go` | NEW | `wa_list_chats`, `wa_get_chat`, `wa_list_messages`, `wa_search_messages` |
| `internal/transport/mcp/tools_contacts.go` | NEW | `wa_list_contacts`, `wa_search_contacts` |
| `internal/transport/mcp/tools_messages.go` | NEW | `wa_send_text`, `wa_send_media`, `wa_get_media`, `wa_edit_message`, `wa_delete_message`, `wa_react`, `wa_list_reactions`, `wa_mark_read`, `wa_typing`, `wa_list_receipts` |
| `internal/transport/mcp/tools_login.go` | NEW | `wa_login_qr`, `wa_login_phone`, `wa_logout` |
| `internal/transport/mcp/tools_groups.go` | NEW | `wa_create_group`, `wa_list_group_members`, `wa_update_group_members`, `wa_leave_group` |
| `internal/transport/mcp/tools_*_test.go` | NEW | One test file per tool group — happy path + error mapping via in-memory transport |
| `internal/transport/mcp/integration_test.go` | NEW | Full streamable-HTTP smoke: chi router + `httptest.Server` + real `mcp.Client` |
| `internal/transport/http/router.go` | MODIFY | Mount `/v1/mcp` inside the protected route group when `cfg.MCP.Enabled` |
| `internal/transport/http/router_test.go` | MODIFY | Cover the enabled + disabled paths |
| `examples/claude-mcp/README.md` | NEW | One-pager: add the daemon as a Claude Code custom connector |
| `examples/claude-mcp/claude_code_config.json` | NEW | Copy-pasteable snippet |
| `README.md` | MODIFY | "Connect from Claude" section after "Run with Docker"; Plan 12 status entry; bump roadmap |
| `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md` | MODIFY | Two wording fixes (strike "*separate* MCP server" in §0 + §1) |

No changes to `service.Service`, `waclient`, `store.*`, or `sse`. The MCP package is additive — removing the chi mount disables it cleanly.

---

## Task 1: Dependency, config, and skeleton package

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/config/config.go` (add `MCP` field + default)
- Modify: `internal/config/config_test.go` (assert default and env override)
- Create: `internal/transport/mcp/server.go`

**Goal:** Pull in the Go MCP SDK, add the on/off config knob, and produce an `http.Handler` that responds to `initialize` requests with zero tools registered. No wiring into the daemon yet.

- [ ] **Step 1: Add the SDK dependency**

Run from repo root:
```bash
go get github.com/modelcontextprotocol/go-sdk@latest
```

Expected: `go.mod` gains a `require github.com/modelcontextprotocol/go-sdk vX.Y.Z` line and `go.sum` is updated. Record the resolved version in the commit message.

- [ ] **Step 2: Confirm the SDK API surface**

Run:
```bash
go doc github.com/modelcontextprotocol/go-sdk/mcp | head -80
go doc github.com/modelcontextprotocol/go-sdk/mcp.Server | head -40
go doc github.com/modelcontextprotocol/go-sdk/mcp.NewStreamableHTTPHandler | head -20
go doc github.com/modelcontextprotocol/go-sdk/mcp.AddTool | head -20
```

Expected: confirms the exported symbols used below (`mcp.NewServer`, `mcp.Implementation`, `mcp.ServerOptions`, `mcp.AddTool`, `mcp.Tool`, `mcp.ToolAnnotations`, `mcp.CallToolRequest`, `mcp.CallToolResult`, `mcp.NewStreamableHTTPHandler`, `mcp.NewInMemoryTransports`, `mcp.Client`).

If a symbol has been renamed in the pinned SDK release, prefer the SDK's current name and adapt the code below uniformly — do not invent a name. Note any rename in the commit message.

- [ ] **Step 3: Add `MCPConfig` to `internal/config/config.go`**

Add the struct (alphabetical with the others, near `MetricsConfig`):

```go
type MCPConfig struct {
    Enabled bool `koanf:"enabled"`
}
```

Add the field to `Config`:

```go
type Config struct {
    DataDir string        `koanf:"data_dir"`
    Server  ServerConfig  `koanf:"server"`
    Auth    AuthConfig    `koanf:"auth"`
    Storage StorageConfig `koanf:"storage"`
    Log     LogConfig     `koanf:"log"`
    Events  EventsConfig  `koanf:"events"`
    Metrics MetricsConfig `koanf:"metrics"`
    HTTP    HTTPConfig    `koanf:"http"`
    MCP     MCPConfig     `koanf:"mcp"`
}
```

Add to the `defaults()` map:

```go
"mcp.enabled": true,
```

- [ ] **Step 4: Add config tests**

Open `internal/config/config_test.go` and add two table entries (or two new tests, matching the file's existing style):

```go
func TestLoad_MCPDefaults(t *testing.T) {
    c, err := config.Load("")
    require.NoError(t, err)
    require.True(t, c.MCP.Enabled, "mcp.enabled should default to true")
}

func TestLoad_MCPEnvOverride(t *testing.T) {
    t.Setenv("WMAPI_MCP__ENABLED", "false")
    c, err := config.Load("")
    require.NoError(t, err)
    require.False(t, c.MCP.Enabled, "WMAPI_MCP__ENABLED=false should turn it off")
}
```

Use whatever import alias the rest of the test file uses (`config "github.com/askarzh/whatsmeow-api/internal/config"` if applicable).

- [ ] **Step 5: Run the config tests**

```bash
go test ./internal/config/... -run TestLoad_MCP -v
```

Expected: both PASS.

- [ ] **Step 6: Create `internal/transport/mcp/server.go`**

```go
// Package mcp serves the daemon's capabilities over an MCP streamable-HTTP
// transport mounted at /v1/mcp. Tool handlers call service.Service directly;
// the package adds no new state.
package mcp

import (
    "log/slog"
    "net/http"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/service"
)

// Deps is the bundle the MCP transport needs.
type Deps struct {
    Service service.Service
    Logger  *slog.Logger
    Version string // forwarded to clients on initialize
}

// New returns an http.Handler that speaks MCP over streamable HTTP.
// The handler is safe to mount under chi and is decoupled from auth —
// the caller wraps it in whatever middleware they need.
func New(d Deps) http.Handler {
    if d.Logger == nil {
        d.Logger = slog.Default()
    }
    return mcpsdk.NewStreamableHTTPHandler(
        func(*http.Request) *mcpsdk.Server { return newServer(d) },
        nil,
    )
}

// newServer constructs a fresh MCP server bound to the given dependencies.
// Tool registration is split into one file per domain to keep each file small.
func newServer(d Deps) *mcpsdk.Server {
    srv := mcpsdk.NewServer(&mcpsdk.Implementation{
        Name:    "whatsmeow-api",
        Version: d.Version,
    }, &mcpsdk.ServerOptions{
        Instructions: instructions,
    })
    // Tool registration lands here in later tasks.
    return srv
}

const instructions = `This server controls a single WhatsApp account through ` +
    `the whatsmeow-api daemon. Chat and group identifiers are WhatsApp JIDs ` +
    `(e.g. "1234567890@s.whatsapp.net" for users, "<group-id>@g.us" for groups). ` +
    `Phone numbers for wa_login_phone are E.164 without the "+". To clear an ` +
    `existing reaction call wa_react with an empty emoji. Messages are ` +
    `searched against the local cache only; remote-only history is not indexed.`
```

> If the SDK's `NewStreamableHTTPHandler` takes a different second-argument shape in the pinned version (e.g. an `*StreamableHTTPOptions`), pass `nil` and adapt the type. The first argument — a function returning a server — is stable.

- [ ] **Step 7: Write a smoke test that constructs the handler**

Create `internal/transport/mcp/server_test.go`:

```go
package mcp_test

import (
    "testing"

    "github.com/stretchr/testify/require"

    mcptransport "github.com/askarzh/whatsmeow-api/internal/transport/mcp"
)

func TestNew_ReturnsNonNilHandler(t *testing.T) {
    h := mcptransport.New(mcptransport.Deps{
        Version: "test",
    })
    require.NotNil(t, h, "MCP handler must be constructible without a Service")
}
```

> The fake `Service` is wired in Task 3. Until then, `New` must not dereference `d.Service`.

- [ ] **Step 8: Run the package**

```bash
go test ./internal/transport/mcp/... -v
go vet ./internal/transport/mcp/...
```

Expected: TestNew_ReturnsNonNilHandler PASS; vet clean.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/config/config.go internal/config/config_test.go \
        internal/transport/mcp/server.go internal/transport/mcp/server_test.go
git commit -m "feat(mcp): add Go MCP SDK, mcp.enabled config, transport skeleton (Plan 12 Task 1)"
```

---

## Task 2: Wire `/v1/mcp` into the chi router

**Files:**
- Modify: `internal/transport/http/router.go`
- Modify: `internal/transport/http/router_test.go`

**Goal:** Mount the MCP handler inside the existing `/v1` protected group, gated by `cfg.MCP.Enabled`. Auth middleware is shared with REST — same `Authorization: Bearer <token>` header.

- [ ] **Step 1: Add a test for the enabled path**

Open `internal/transport/http/router_test.go`. Add at the bottom (use whatever package-level helpers already exist in the file):

```go
func TestRouter_MountsMCPWhenEnabled(t *testing.T) {
    r := httpapi.NewRouter(httpapi.Deps{
        Config: config.Config{
            Auth: config.AuthConfig{Token: "s3cret"},
            MCP:  config.MCPConfig{Enabled: true},
        },
        Service: fakeStatusSvc{},
    })

    req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
    req.Header.Set("Authorization", "Bearer s3cret")
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    // The SDK rejects the empty body with a JSON-RPC error, but the *route*
    // exists — anything other than 404 confirms mounting.
    require.NotEqual(t, http.StatusNotFound, w.Code, "expected /v1/mcp to be mounted; got 404")
}

func TestRouter_MCPDisabledReturns404(t *testing.T) {
    r := httpapi.NewRouter(httpapi.Deps{
        Config: config.Config{
            Auth: config.AuthConfig{Token: "s3cret"},
            MCP:  config.MCPConfig{Enabled: false},
        },
        Service: fakeStatusSvc{},
    })

    req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
    req.Header.Set("Authorization", "Bearer s3cret")
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    require.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_MCPRequiresBearer(t *testing.T) {
    r := httpapi.NewRouter(httpapi.Deps{
        Config: config.Config{
            Auth: config.AuthConfig{Token: "s3cret"},
            MCP:  config.MCPConfig{Enabled: true},
        },
        Service: fakeStatusSvc{},
    })

    req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{}`))
    // no Authorization header
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    require.Equal(t, http.StatusUnauthorized, w.Code)
    require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
}
```

If `strings` or `httptest` are not already imported, add them.

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./internal/transport/http/... -run TestRouter_MCP -v
```

Expected: all three FAIL — `/v1/mcp` returns 404 because nothing's mounted yet.

- [ ] **Step 3: Modify `internal/transport/http/router.go`**

Add the import:

```go
mcpapi "github.com/askarzh/whatsmeow-api/internal/transport/mcp"
```

Inside the protected-route closure (the block after `r.Use(RequireBearerToken(...))`), append at the end:

```go
            if d.Config.MCP.Enabled {
                r.Mount("/mcp", mcpapi.New(mcpapi.Deps{
                    Service: d.Service,
                    Logger:  d.Logger,
                    Version: Version, // see Step 4
                }))
            }
```

- [ ] **Step 4: Plumb a build-time version string**

If `internal/transport/http` doesn't already expose a `Version` constant, add it at the top of `router.go`:

```go
// Version is the daemon version surfaced over MCP initialize. It is overridden
// at build time via -ldflags="-X .../http.Version=<tag>".
var Version = "dev"
```

(Plan 11's Dockerfile already passes `-X main.version=${VERSION}` — leave that alone; if `cmd/whatsmeow-api` doesn't yet propagate that to this package, that's fine, the constant stays "dev" until a follow-up plan wires it. Acceptable for v1.)

- [ ] **Step 5: Re-run the tests**

```bash
go test ./internal/transport/http/... -run TestRouter_MCP -v
```

Expected: all three PASS.

- [ ] **Step 6: Run the full http transport tests**

```bash
go test ./internal/transport/http/... -count=1
```

Expected: PASS — no regression. If a pre-existing test asserts the exact list of mounted routes, update it to include `/v1/mcp`.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/http/router.go internal/transport/http/router_test.go
git commit -m "feat(mcp): mount /v1/mcp under the protected route group (Plan 12 Task 2)"
```

---

## Task 3: Error-mapping wrapper + first read-only tool (`wa_status`) as template

**Files:**
- Create: `internal/transport/mcp/errors.go`
- Create: `internal/transport/mcp/errors_test.go`
- Create: `internal/transport/mcp/tools_status.go`
- Create: `internal/transport/mcp/tools_status_test.go`
- Modify: `internal/transport/mcp/server.go` (call `registerStatusTools`)

**Goal:** Lock in the patterns that the remaining six tool-registration tasks copy: typed input/output structs, `mcp.AddTool` call shape, error-mapping wrapper, in-memory-transport test harness.

- [ ] **Step 1: Write `errors.go`**

```go
package mcp

import (
    "errors"
    "fmt"
    "log/slog"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/service"
    "github.com/askarzh/whatsmeow-api/internal/store"
)

// mapErr converts a service-layer error into either an MCP "tool error"
// (CallToolResult with IsError=true) or a transport-level error that the
// SDK turns into a JSON-RPC internal-error reply.
//
// Bucket assignment:
//   - ErrInvalidRequest        → tool error, "invalid request: <wrapped msg>"
//   - ErrForbidden             → tool error, "forbidden: <wrapped msg>"
//   - store.ErrNotFound        → tool error, "not found: <wrapped msg>"
//   - any other non-nil error  → transport error (logged, generic "internal error" to client)
//
// Callers pattern:
//
//      result, transportErr := mapErr(err, logger)
//      if transportErr != nil { return nil, Out{}, transportErr }
//      if result != nil { return result, Out{}, nil }
//      // happy path
func mapErr(err error, logger *slog.Logger) (*mcpsdk.CallToolResult, error) {
    if err == nil {
        return nil, nil
    }
    switch {
    case errors.Is(err, service.ErrInvalidRequest):
        return toolErr(fmt.Sprintf("invalid request: %s", err.Error())), nil
    case errors.Is(err, service.ErrForbidden):
        return toolErr(fmt.Sprintf("forbidden: %s", err.Error())), nil
    case errors.Is(err, store.ErrNotFound):
        return toolErr(fmt.Sprintf("not found: %s", err.Error())), nil
    default:
        if logger != nil {
            logger.Error("mcp tool error", "err", err)
        }
        return nil, fmt.Errorf("internal error")
    }
}

func toolErr(msg string) *mcpsdk.CallToolResult {
    return &mcpsdk.CallToolResult{
        IsError: true,
        Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
    }
}
```

> Adapt the `Content`/`TextContent` constructors to whatever names the pinned SDK exposes (some versions use `mcp.NewTextContent("...")`). Do not invent a name — use `go doc github.com/modelcontextprotocol/go-sdk/mcp.TextContent` to confirm.

- [ ] **Step 2: Write `errors_test.go`**

```go
package mcp

import (
    "errors"
    "fmt"
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/askarzh/whatsmeow-api/internal/service"
    "github.com/askarzh/whatsmeow-api/internal/store"
)

func TestMapErr_Nil(t *testing.T) {
    res, err := mapErr(nil, nil)
    require.Nil(t, res)
    require.NoError(t, err)
}

func TestMapErr_InvalidRequest(t *testing.T) {
    in := fmt.Errorf("%w: text is required", service.ErrInvalidRequest)
    res, err := mapErr(in, nil)
    require.NoError(t, err)
    require.NotNil(t, res)
    require.True(t, res.IsError)
    require.Contains(t, res.Content[0].(asTexter).GetText(), "invalid request:")
    require.Contains(t, res.Content[0].(asTexter).GetText(), "text is required")
}

func TestMapErr_Forbidden(t *testing.T) {
    res, err := mapErr(fmt.Errorf("%w: not owner", service.ErrForbidden), nil)
    require.NoError(t, err)
    require.True(t, res.IsError)
    require.Contains(t, res.Content[0].(asTexter).GetText(), "forbidden:")
}

func TestMapErr_NotFound(t *testing.T) {
    res, err := mapErr(fmt.Errorf("%w: id=abc", store.ErrNotFound), nil)
    require.NoError(t, err)
    require.True(t, res.IsError)
    require.Contains(t, res.Content[0].(asTexter).GetText(), "not found:")
}

func TestMapErr_Unknown(t *testing.T) {
    res, err := mapErr(errors.New("kaboom"), nil)
    require.Nil(t, res)
    require.EqualError(t, err, "internal error")
}

// asTexter abstracts over whichever Content type the SDK uses for text so the
// test doesn't break if the SDK renames TextContent.
type asTexter interface{ GetText() string }
```

> If the SDK's text-content type exposes the string via a different accessor (e.g. a public `Text` field rather than `GetText()`), update the `asTexter` interface accordingly — keep all reads through the interface so the file has one place to patch.

- [ ] **Step 3: Write `tools_status.go`**

```go
package mcp

import (
    "context"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type waStatusInput struct{}

type waStatusOutput struct {
    Status string `json:"status" jsonschema:"description=Connection state: one of disconnected, connecting, connected, logged_out"`
    JID    string `json:"jid,omitempty" jsonschema:"description=Logged-in WhatsApp JID; empty until paired"`
}

type waStatsOutput struct {
    Chats     int64 `json:"chats"`
    Messages  int64 `json:"messages"`
    Contacts  int64 `json:"contacts"`
    Reactions int64 `json:"reactions"`
    Receipts  int64 `json:"receipts"`
    MediaRefs int64 `json:"media_refs"`
}

func registerStatusTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_status",
        Description: "Return the daemon's current WhatsApp connection state and the logged-in JID. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{
            ReadOnlyHint:    true,
            IdempotentHint:  true,
            OpenWorldHint:   true,
        },
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ waStatusInput) (*mcpsdk.CallToolResult, waStatusOutput, error) {
        s, err := d.Service.Status(ctx)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waStatusOutput{}, terr
        }
        return nil, waStatusOutput{Status: s.State.String(), JID: s.JID}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_stats",
        Description: "Return aggregate counts of the local cache (chats, messages, contacts, reactions, receipts, media refs). Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{
            ReadOnlyHint:   true,
            IdempotentHint: true,
        },
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waStatsOutput, error) {
        s, err := d.Service.Stats(ctx)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waStatsOutput{}, terr
        }
        return nil, waStatsOutput{
            Chats: s.Chats, Messages: s.Messages, Contacts: s.Contacts,
            Reactions: s.Reactions, Receipts: s.Receipts, MediaRefs: s.MediaRefs,
        }, nil
    })
}
```

> Two field-name reconciliations:
> - `s.State.String()` — `waclient.Status` has a typed `State` field with a `String()` method. If it's named differently (e.g. `Phase`, or already a string), use whatever the existing REST handler uses in `internal/transport/http/status.go`.
> - `service.Stats` field names — copy verbatim from `internal/service/service.go` `type Stats struct{…}`. Update both struct and call site uniformly if the names differ.

- [ ] **Step 4: Update `server.go` to register the status tools**

In `internal/transport/mcp/server.go`, change `newServer` to call the registrar:

```go
func newServer(d Deps) *mcpsdk.Server {
    srv := mcpsdk.NewServer(&mcpsdk.Implementation{
        Name:    "whatsmeow-api",
        Version: d.Version,
    }, &mcpsdk.ServerOptions{
        Instructions: instructions,
    })
    registerStatusTools(srv, d)
    // additional registrars added in Tasks 4-6
    return srv
}
```

- [ ] **Step 5: Write `tools_status_test.go`**

```go
package mcp

import (
    "context"
    "encoding/json"
    "errors"
    "testing"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/stretchr/testify/require"

    "github.com/askarzh/whatsmeow-api/internal/service"
    "github.com/askarzh/whatsmeow-api/internal/waclient"
)

// fakeService is a hand-written service.Service stub used across the MCP tool
// tests. Each test fills in only the fields it exercises.
type fakeService struct {
    service.Service // embedded zero — calls to unset methods will panic with nil-deref, which is fine: a test that hits an unset method is a bug.
    statusFn  func(context.Context) (waclient.Status, error)
    statsFn   func(context.Context) (service.Stats, error)
}

func (f *fakeService) Status(ctx context.Context) (waclient.Status, error) { return f.statusFn(ctx) }
func (f *fakeService) Stats(ctx context.Context) (service.Stats, error)    { return f.statsFn(ctx) }

// inMemoryClient wires an mcp.Server (with our tools registered) to an
// mcp.Client over the SDK's in-memory transport pair.
func inMemoryClient(t *testing.T, svc service.Service) (context.Context, *mcpsdk.ClientSession) {
    t.Helper()
    ctx, cancel := context.WithCancel(context.Background())
    t.Cleanup(cancel)

    srv := newServer(Deps{Service: svc})

    sTr, cTr := mcpsdk.NewInMemoryTransports()
    go func() { _ = srv.Run(ctx, sTr) }()

    client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcp-test", Version: "test"}, nil)
    session, err := client.Connect(ctx, cTr, nil)
    require.NoError(t, err)
    t.Cleanup(func() { _ = session.Close() })
    return ctx, session
}

func TestWAStatus_HappyPath(t *testing.T) {
    svc := &fakeService{
        statusFn: func(ctx context.Context) (waclient.Status, error) {
            return waclient.Status{State: waclient.StateConnected, JID: "1@s.whatsapp.net"}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)

    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_status",
        Arguments: map[string]any{},
    })
    require.NoError(t, err)
    require.False(t, res.IsError, "expected non-error result; got: %+v", res)

    var out struct {
        Status string `json:"status"`
        JID    string `json:"jid"`
    }
    require.NoError(t, json.Unmarshal(res.StructuredContent.([]byte), &out)) // see note
    require.Equal(t, "connected", out.Status)
    require.Equal(t, "1@s.whatsapp.net", out.JID)
}

func TestWAStatus_ServiceError(t *testing.T) {
    svc := &fakeService{
        statusFn: func(ctx context.Context) (waclient.Status, error) {
            return waclient.Status{}, errors.New("upstream gone")
        },
    }
    ctx, session := inMemoryClient(t, svc)

    _, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_status"})
    require.Error(t, err, "an unknown-error path should bubble up as a JSON-RPC error, not a tool result")
}

func TestWAStats_HappyPath(t *testing.T) {
    svc := &fakeService{
        statsFn: func(ctx context.Context) (service.Stats, error) {
            return service.Stats{Chats: 7, Messages: 42}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_stats"})
    require.NoError(t, err)
    require.False(t, res.IsError)
    // Detailed field check omitted for brevity; verified in integration_test.
}
```

> Two SDK reconciliations the implementer should resolve via `go doc` before/after writing:
> 1. **Reading structured output.** Some SDK versions return JSON-marshalled bytes in `res.StructuredContent`, some return a `map[string]any`, some surface the typed-out struct via a generic helper. Pick the accessor that the SDK actually exposes and use it uniformly across this file and the per-tool tests in later tasks.
> 2. **`CallToolParams` field names.** Confirm `Name` and `Arguments` are the exact field names — older SDK versions used `Params` or `Input`.

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/transport/mcp/... -v
```

Expected: every test PASS. If `TestWAStatus_HappyPath` fails on the structured-content unmarshal, follow the note above and pick the SDK's actual accessor.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/mcp/errors.go internal/transport/mcp/errors_test.go \
        internal/transport/mcp/tools_status.go internal/transport/mcp/tools_status_test.go \
        internal/transport/mcp/server.go
git commit -m "feat(mcp): error mapping + wa_status + wa_stats tools (Plan 12 Task 3)"
```

---

## Task 4: Remaining read-only tools — chats, contacts, reactions, receipts, media

**Files:**
- Create: `internal/transport/mcp/tools_chats.go`
- Create: `internal/transport/mcp/tools_chats_test.go`
- Create: `internal/transport/mcp/tools_contacts.go`
- Create: `internal/transport/mcp/tools_contacts_test.go`
- Modify: `internal/transport/mcp/tools_messages.go` (read-only halves: `wa_list_reactions`, `wa_list_receipts`, `wa_get_media`)
- Modify: `internal/transport/mcp/tools_messages_test.go`
- Modify: `internal/transport/mcp/server.go` (call new registrars)

Wait — `tools_messages.go` is created in this task, not yet existing. Read-only message-side tools land here; write-side tools land in Task 5 by adding to the same file. Keeping all message-id-keyed tools in one file matches how `internal/transport/http/messages.go` is structured.

**Goal:** Register the 10 read-only tools beyond status/stats:
`wa_list_chats`, `wa_get_chat`, `wa_list_messages`, `wa_search_messages`, `wa_list_contacts`, `wa_search_contacts`, `wa_list_reactions`, `wa_list_receipts`, `wa_get_media`. (Stats was registered in Task 3.)

- [ ] **Step 1: Write `tools_chats.go`**

```go
package mcp

import (
    "context"
    "time"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/store"
)

type waListChatsInput struct {
    Before          string `json:"before,omitempty" jsonschema:"description=RFC3339 timestamp; only chats with last-message-at strictly before this are returned. Omit for the most-recent page."`
    Limit           int    `json:"limit,omitempty" jsonschema:"description=Max chats to return (1..200). Default 50."`
    IncludeArchived bool   `json:"include_archived,omitempty"`
}

type waChatJSON struct {
    JID           string    `json:"jid"`
    Name          string    `json:"name"`
    Archived      bool      `json:"archived"`
    LastMessageAt time.Time `json:"last_message_at,omitempty"`
    UnreadCount   int       `json:"unread_count,omitempty"`
}

type waListChatsOutput struct {
    Chats []waChatJSON `json:"chats"`
}

type waGetChatInput struct {
    ChatJID string `json:"chat_jid" jsonschema:"description=WhatsApp JID of the chat, e.g. 1234567890@s.whatsapp.net or <group-id>@g.us"`
}

type waGetChatOutput struct {
    Chat waChatJSON `json:"chat"`
}

type waListMessagesInput struct {
    ChatJID string `json:"chat_jid"`
    Before  string `json:"before,omitempty" jsonschema:"description=RFC3339 timestamp; only messages before this are returned."`
    Limit   int    `json:"limit,omitempty" jsonschema:"description=Max messages to return (1..200). Default 50."`
}

type waMessageJSON struct {
    ID        string    `json:"id"`
    ChatJID   string    `json:"chat_jid"`
    SenderJID string    `json:"sender_jid"`
    Text      string    `json:"text,omitempty"`
    FromMe    bool      `json:"from_me"`
    Timestamp time.Time `json:"timestamp"`
}

type waListMessagesOutput struct {
    Messages []waMessageJSON `json:"messages"`
}

type waSearchMessagesInput struct {
    Query string `json:"query"`
    Limit int    `json:"limit,omitempty"`
}

type waSearchMessagesOutput struct {
    Messages []waMessageJSON `json:"messages"`
}

func registerChatTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_chats",
        Description: "List chats ordered by last-message-at descending. Use 'before' (RFC3339) to paginate older. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListChatsInput) (*mcpsdk.CallToolResult, waListChatsOutput, error) {
        before, perr := parseRFC3339OrZero(in.Before)
        if perr != nil {
            return toolErr("invalid request: before must be RFC3339"), waListChatsOutput{}, nil
        }
        rows, err := d.Service.ListChats(ctx, before, in.Limit, in.IncludeArchived)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListChatsOutput{}, terr
        }
        return nil, waListChatsOutput{Chats: chatRowsToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_get_chat",
        Description: "Fetch a single chat by JID. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGetChatInput) (*mcpsdk.CallToolResult, waGetChatOutput, error) {
        row, err := d.Service.GetChat(ctx, in.ChatJID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waGetChatOutput{}, terr
        }
        return nil, waGetChatOutput{Chat: chatRowToJSON(row)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_messages",
        Description: "List messages in a chat ordered by timestamp descending. Use 'before' (RFC3339) to paginate older. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waListMessagesInput) (*mcpsdk.CallToolResult, waListMessagesOutput, error) {
        before, perr := parseRFC3339OrZero(in.Before)
        if perr != nil {
            return toolErr("invalid request: before must be RFC3339"), waListMessagesOutput{}, nil
        }
        rows, err := d.Service.ListMessages(ctx, in.ChatJID, before, in.Limit)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListMessagesOutput{}, terr
        }
        return nil, waListMessagesOutput{Messages: messageRowsToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_search_messages",
        Description: "Full-text search messages across all chats in the local cache. Returns up to 'limit' matches.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSearchMessagesInput) (*mcpsdk.CallToolResult, waSearchMessagesOutput, error) {
        rows, err := d.Service.SearchMessages(ctx, in.Query, in.Limit)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waSearchMessagesOutput{}, terr
        }
        return nil, waSearchMessagesOutput{Messages: messageRowsToJSON(rows)}, nil
    })
}

// --- helpers (also used by tools_messages.go) ---

func parseRFC3339OrZero(s string) (time.Time, error) {
    if s == "" {
        return time.Time{}, nil
    }
    return time.Parse(time.RFC3339, s)
}

func chatRowToJSON(c store.Chat) waChatJSON {
    return waChatJSON{
        JID:           c.JID,
        Name:          c.Name,
        Archived:      c.Archived,
        LastMessageAt: c.LastMessageAt,
        UnreadCount:   c.UnreadCount,
    }
}
func chatRowsToJSON(rows []store.Chat) []waChatJSON {
    out := make([]waChatJSON, 0, len(rows))
    for _, c := range rows { out = append(out, chatRowToJSON(c)) }
    return out
}

func messageRowToJSON(m store.Message) waMessageJSON {
    return waMessageJSON{
        ID:        m.ID,
        ChatJID:   m.ChatJID,
        SenderJID: m.SenderJID,
        Text:      m.Text,
        FromMe:    m.FromMe,
        Timestamp: m.Timestamp,
    }
}
func messageRowsToJSON(rows []store.Message) []waMessageJSON {
    out := make([]waMessageJSON, 0, len(rows))
    for _, m := range rows { out = append(out, messageRowToJSON(m)) }
    return out
}
```

> Field names on `store.Chat` and `store.Message` should match what `internal/transport/http/chats.go` and `messages.go` use. If a field is named `From` instead of `FromMe`, mirror the REST handler — do not invent a new shape.

- [ ] **Step 2: Write `tools_contacts.go`**

```go
package mcp

import (
    "context"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/store"
)

type waContactJSON struct {
    JID       string `json:"jid"`
    Name      string `json:"name,omitempty"`
    PushName  string `json:"push_name,omitempty"`
    IsBusiness bool   `json:"is_business,omitempty"`
}

type waListContactsOutput struct {
    Contacts []waContactJSON `json:"contacts"`
}

type waSearchContactsInput struct {
    Query string `json:"query"`
    Limit int    `json:"limit,omitempty"`
}

type waSearchContactsOutput struct {
    Contacts []waContactJSON `json:"contacts"`
}

func registerContactTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_contacts",
        Description: "Return all locally-cached contacts. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waListContactsOutput, error) {
        rows, err := d.Service.ListContacts(ctx)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListContactsOutput{}, terr
        }
        return nil, waListContactsOutput{Contacts: contactRowsToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_search_contacts",
        Description: "Substring search across contact name / push_name / phone. Returns up to 'limit' matches.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSearchContactsInput) (*mcpsdk.CallToolResult, waSearchContactsOutput, error) {
        rows, err := d.Service.SearchContacts(ctx, in.Query, in.Limit)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waSearchContactsOutput{}, terr
        }
        return nil, waSearchContactsOutput{Contacts: contactRowsToJSON(rows)}, nil
    })
}

func contactRowToJSON(c store.Contact) waContactJSON {
    return waContactJSON{
        JID: c.JID, Name: c.Name, PushName: c.PushName, IsBusiness: c.IsBusiness,
    }
}
func contactRowsToJSON(rows []store.Contact) []waContactJSON {
    out := make([]waContactJSON, 0, len(rows))
    for _, c := range rows { out = append(out, contactRowToJSON(c)) }
    return out
}
```

> Reconcile `store.Contact` field names with `internal/transport/http/contacts.go`.

- [ ] **Step 3: Write `tools_messages.go` (read-only portion)**

```go
package mcp

import (
    "context"
    "time"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/store"
)

type waMessageIDInput struct {
    MessageID string `json:"message_id"`
}

type waReactionJSON struct {
    SenderJID string    `json:"sender_jid"`
    Emoji     string    `json:"emoji"`
    Timestamp time.Time `json:"timestamp"`
}

type waListReactionsOutput struct {
    Reactions []waReactionJSON `json:"reactions"`
}

type waReceiptJSON struct {
    SenderJID string    `json:"sender_jid"`
    Type      string    `json:"type"`
    Timestamp time.Time `json:"timestamp"`
}

type waListReceiptsOutput struct {
    Receipts []waReceiptJSON `json:"receipts"`
}

type waMediaRefOutput struct {
    MessageID string `json:"message_id"`
    Kind      string `json:"kind"`
    Path      string `json:"path"`
    MIME      string `json:"mime"`
    Filename  string `json:"filename,omitempty"`
    Size      int64  `json:"size"`
    Caption   string `json:"caption,omitempty"`
}

func registerReadOnlyMessageTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_reactions",
        Description: "List all reactions on a given message. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waListReactionsOutput, error) {
        rows, err := d.Service.ListReactions(ctx, in.MessageID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListReactionsOutput{}, terr
        }
        return nil, waListReactionsOutput{Reactions: reactionRowsToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_receipts",
        Description: "List delivery/read receipts for a given message. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waListReceiptsOutput, error) {
        rows, err := d.Service.ListReceipts(ctx, in.MessageID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListReceiptsOutput{}, terr
        }
        return nil, waListReceiptsOutput{Receipts: receiptRowsToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_get_media",
        Description: "Return metadata + disk path for the media attached to a message. Use this after a media message arrives via SSE.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waMediaRefOutput, error) {
        ref, err := d.Service.GetMediaRef(ctx, in.MessageID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waMediaRefOutput{}, terr
        }
        return nil, waMediaRefOutput{
            MessageID: ref.MessageID, Kind: ref.Kind, Path: ref.Path,
            MIME: ref.MIME, Filename: ref.Filename, Size: ref.Size, Caption: ref.Caption,
        }, nil
    })
}

func reactionRowsToJSON(rows []store.Reaction) []waReactionJSON {
    out := make([]waReactionJSON, 0, len(rows))
    for _, r := range rows {
        out = append(out, waReactionJSON{SenderJID: r.SenderJID, Emoji: r.Emoji, Timestamp: r.Timestamp})
    }
    return out
}

func receiptRowsToJSON(rows []store.Receipt) []waReceiptJSON {
    out := make([]waReceiptJSON, 0, len(rows))
    for _, r := range rows {
        out = append(out, waReceiptJSON{SenderJID: r.SenderJID, Type: r.Type, Timestamp: r.Timestamp})
    }
    return out
}
```

- [ ] **Step 4: Register the new tool groups**

Update `internal/transport/mcp/server.go`:

```go
func newServer(d Deps) *mcpsdk.Server {
    srv := mcpsdk.NewServer(&mcpsdk.Implementation{
        Name:    "whatsmeow-api",
        Version: d.Version,
    }, &mcpsdk.ServerOptions{
        Instructions: instructions,
    })
    registerStatusTools(srv, d)
    registerChatTools(srv, d)
    registerContactTools(srv, d)
    registerReadOnlyMessageTools(srv, d)
    // tasks 5–6 add: registerWriteMessageTools, registerLoginTools, registerGroupTools
    return srv
}
```

- [ ] **Step 5: Write `tools_chats_test.go`**

```go
package mcp

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/askarzh/whatsmeow-api/internal/store"
    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (f *fakeService) ListChats(ctx context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
    return f.listChatsFn(ctx, before, limit, includeArchived)
}
func (f *fakeService) GetChat(ctx context.Context, jid string) (store.Chat, error) {
    return f.getChatFn(ctx, jid)
}

// Extend fakeService with the fields above. Add them to the struct in tools_status_test.go.

func TestWAListChats_HappyPath(t *testing.T) {
    svc := &fakeService{
        listChatsFn: func(ctx context.Context, before time.Time, limit int, _ bool) ([]store.Chat, error) {
            require.True(t, before.IsZero())
            require.Equal(t, 0, limit)
            return []store.Chat{{JID: "1@s.whatsapp.net", Name: "Alice"}}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)

    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_list_chats"})
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWAListChats_InvalidBefore(t *testing.T) {
    svc := &fakeService{
        listChatsFn: func(context.Context, time.Time, int, bool) ([]store.Chat, error) { t.Fatal("not reached"); return nil, nil },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_list_chats",
        Arguments: map[string]any{"before": "not-a-timestamp"},
    })
    require.NoError(t, err)
    require.True(t, res.IsError, "expected tool error for invalid 'before'")
}

func TestWAGetChat_NotFound(t *testing.T) {
    svc := &fakeService{
        getChatFn: func(context.Context, string) (store.Chat, error) {
            return store.Chat{}, store.ErrNotFound
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_get_chat",
        Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net"},
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}
```

- [ ] **Step 6: Extend `fakeService` in `tools_status_test.go`**

Add the new function fields to the struct (and remove the embedded `service.Service` since enough methods are now implemented):

```go
type fakeService struct {
    statusFn        func(context.Context) (waclient.Status, error)
    statsFn         func(context.Context) (service.Stats, error)
    listChatsFn     func(context.Context, time.Time, int, bool) ([]store.Chat, error)
    getChatFn       func(context.Context, string) (store.Chat, error)
    listMessagesFn  func(context.Context, string, time.Time, int) ([]store.Message, error)
    searchMessagesFn func(context.Context, string, int) ([]store.Message, error)
    listContactsFn  func(context.Context) ([]store.Contact, error)
    searchContactsFn func(context.Context, string, int) ([]store.Contact, error)
    listReactionsFn func(context.Context, string) ([]store.Reaction, error)
    listReceiptsFn  func(context.Context, string) ([]store.Receipt, error)
    getMediaRefFn   func(context.Context, string) (store.MediaRef, error)
    // Tasks 5-6 add: sendTextFn, sendMediaFn, editMessageFn, deleteMessageFn,
    // sendReactionFn, markReadFn, sendTypingFn, loginQRFn, loginPhoneFn,
    // logoutFn, createGroupFn, listGroupMembersFn, updateGroupMembersFn, leaveGroupFn.
}
```

Implement the bare-minimum methods for the remaining `Service` interface so the type still satisfies it (each delegates to its `Fn` field; if nil, panic with a "fakeService method not stubbed" message). The pattern keeps test failures clear when a test forgets to wire a fake.

> A complete `fakeService` implementing all 25 `Service` methods lives in `tools_status_test.go` after this task. Each method follows the same shape:
>
> ```go
> func (f *fakeService) Status(ctx context.Context) (waclient.Status, error) {
>     if f.statusFn == nil { panic("fakeService.Status not stubbed") }
>     return f.statusFn(ctx)
> }
> ```

- [ ] **Step 7: Write `tools_contacts_test.go`**

```go
package mcp

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/askarzh/whatsmeow-api/internal/store"
)

func TestWAListContacts_HappyPath(t *testing.T) {
    svc := &fakeService{
        listContactsFn: func(context.Context) ([]store.Contact, error) {
            return []store.Contact{{JID: "1@s.whatsapp.net", Name: "Alice"}}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_list_contacts"})
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWASearchContacts_PassesQueryAndLimit(t *testing.T) {
    captured := struct{ q string; lim int }{}
    svc := &fakeService{
        searchContactsFn: func(_ context.Context, q string, lim int) ([]store.Contact, error) {
            captured.q = q; captured.lim = lim
            return nil, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    _, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_search_contacts",
        Arguments: map[string]any{"query": "ali", "limit": 5},
    })
    require.NoError(t, err)
    require.Equal(t, "ali", captured.q)
    require.Equal(t, 5, captured.lim)
}
```

- [ ] **Step 8: Write `tools_messages_test.go` (read-only halves only — write-side lands in Task 5)**

```go
package mcp

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/askarzh/whatsmeow-api/internal/store"
)

func TestWAListReactions_HappyPath(t *testing.T) {
    svc := &fakeService{
        listReactionsFn: func(_ context.Context, id string) ([]store.Reaction, error) {
            require.Equal(t, "msg-1", id)
            return []store.Reaction{{SenderJID: "x@s.whatsapp.net", Emoji: "👍"}}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_list_reactions",
        Arguments: map[string]any{"message_id": "msg-1"},
    })
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWAGetMedia_NotFound(t *testing.T) {
    svc := &fakeService{
        getMediaRefFn: func(context.Context, string) (store.MediaRef, error) {
            return store.MediaRef{}, store.ErrNotFound
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_get_media",
        Arguments: map[string]any{"message_id": "nope"},
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}
```

- [ ] **Step 9: Run all MCP tests**

```bash
go test ./internal/transport/mcp/... -count=1 -v
go vet ./internal/transport/mcp/...
```

Expected: PASS, vet clean.

- [ ] **Step 10: Commit**

```bash
git add internal/transport/mcp/tools_chats.go internal/transport/mcp/tools_chats_test.go \
        internal/transport/mcp/tools_contacts.go internal/transport/mcp/tools_contacts_test.go \
        internal/transport/mcp/tools_messages.go internal/transport/mcp/tools_messages_test.go \
        internal/transport/mcp/tools_status_test.go \
        internal/transport/mcp/server.go
git commit -m "feat(mcp): read-only tools — chats, contacts, reactions, receipts, media (Plan 12 Task 4)"
```

---

## Task 5: Write-side message tools — send, edit, delete, react, mark_read, typing

**Files:**
- Modify: `internal/transport/mcp/tools_messages.go` (append `registerWriteMessageTools`)
- Modify: `internal/transport/mcp/tools_messages_test.go`
- Modify: `internal/transport/mcp/tools_status_test.go` (extend `fakeService` with send-side methods, if not already)
- Modify: `internal/transport/mcp/server.go` (call `registerWriteMessageTools`)

**Goal:** Register the 7 write-side message tools: `wa_send_text`, `wa_send_media`, `wa_edit_message`, `wa_delete_message`, `wa_react`, `wa_mark_read`, `wa_typing`.

- [ ] **Step 1: Append to `tools_messages.go`**

```go
type waSendTextInput struct {
    ChatJID string `json:"chat_jid"`
    Text    string `json:"text"`
    ReplyTo string `json:"reply_to,omitempty" jsonschema:"description=Message ID to reply to (optional)."`
}

type waSendTextOutput struct {
    Message waMessageJSON `json:"message"`
}

type waSendMediaInput struct {
    ChatJID    string `json:"chat_jid"`
    Kind       string `json:"kind" jsonschema:"description=one of: image, document"`
    BodyBase64 string `json:"body_base64" jsonschema:"description=base64-encoded media bytes. Subject to WMAPI_HTTP__MAX_BODY_BYTES."`
    Caption    string `json:"caption,omitempty"`
    MIMEType   string `json:"mime_type,omitempty" jsonschema:"description=MIME type; sniffed if omitted."`
    Filename   string `json:"filename,omitempty" jsonschema:"description=Required when kind=document."`
}

type waSendMediaOutput struct {
    Message waMessageJSON `json:"message"`
}

type waEditMessageInput struct {
    MessageID string `json:"message_id"`
    Text      string `json:"text"`
}

type waEditMessageOutput struct {
    Message waMessageJSON `json:"message"`
}

type waReactInput struct {
    MessageID string `json:"message_id"`
    Emoji     string `json:"emoji" jsonschema:"description=Single emoji. Empty string clears the existing reaction."`
}

type waTypingInput struct {
    ChatJID string `json:"chat_jid"`
    State   string `json:"state" jsonschema:"description=one of: composing, paused"`
}

type waOK struct {
    OK bool `json:"ok"`
}

func registerWriteMessageTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_send_text",
        Description: "Send a text message to a chat (1:1 or group). Returns the persisted message row.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSendTextInput) (*mcpsdk.CallToolResult, waSendTextOutput, error) {
        m, err := d.Service.SendText(ctx, in.ChatJID, in.Text, in.ReplyTo)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waSendTextOutput{}, terr
        }
        return nil, waSendTextOutput{Message: messageRowToJSON(m)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_send_media",
        Description: "Send a media message (image or document) to a chat. Body is base64-encoded; prefer small payloads — large files should be sent via the REST /v1/media multipart endpoint.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waSendMediaInput) (*mcpsdk.CallToolResult, waSendMediaOutput, error) {
        body, decodeErr := base64.StdEncoding.DecodeString(in.BodyBase64)
        if decodeErr != nil {
            return toolErr("invalid request: body_base64 must be standard base64"), waSendMediaOutput{}, nil
        }
        m, err := d.Service.SendMedia(ctx, service.SendMediaRequest{
            ChatJID:  in.ChatJID,
            Kind:     in.Kind,
            Caption:  in.Caption,
            Filename: in.Filename,
            MIME:     in.MIMEType,
            Body:     body,
        })
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waSendMediaOutput{}, terr
        }
        return nil, waSendMediaOutput{Message: messageRowToJSON(m)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_edit_message",
        Description: "Edit a previously-sent text message you own. Returns the updated message.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waEditMessageInput) (*mcpsdk.CallToolResult, waEditMessageOutput, error) {
        m, err := d.Service.EditMessage(ctx, in.MessageID, in.Text)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waEditMessageOutput{}, terr
        }
        return nil, waEditMessageOutput{Message: messageRowToJSON(m)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_delete_message",
        Description: "Delete a message you own (or revoke for everyone). Irreversible on the network.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, DestructiveHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waOK, error) {
        err := d.Service.DeleteMessage(ctx, in.MessageID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waOK{}, terr
        }
        return nil, waOK{OK: true}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_react",
        Description: "Add or replace a reaction on a message. Pass emoji=\"\" to clear an existing reaction.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waReactInput) (*mcpsdk.CallToolResult, waOK, error) {
        err := d.Service.SendReaction(ctx, in.MessageID, in.Emoji)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waOK{}, terr
        }
        return nil, waOK{OK: true}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_mark_read",
        Description: "Mark an inbound message (and prior messages in the chat) as read on the network.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waMessageIDInput) (*mcpsdk.CallToolResult, waOK, error) {
        err := d.Service.MarkMessageRead(ctx, in.MessageID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waOK{}, terr
        }
        return nil, waOK{OK: true}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_typing",
        Description: "Send a typing-presence indicator to a chat. state must be 'composing' or 'paused'.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waTypingInput) (*mcpsdk.CallToolResult, waOK, error) {
        err := d.Service.SendTyping(ctx, in.ChatJID, in.State)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waOK{}, terr
        }
        return nil, waOK{OK: true}, nil
    })
}
```

Imports to add at the top of `tools_messages.go`:

```go
import (
    "context"
    "encoding/base64"
    "time"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/service"
    "github.com/askarzh/whatsmeow-api/internal/store"
)
```

- [ ] **Step 2: Wire the registrar**

In `internal/transport/mcp/server.go`:

```go
    registerReadOnlyMessageTools(srv, d)
    registerWriteMessageTools(srv, d)
```

- [ ] **Step 3: Extend `fakeService` in `tools_status_test.go`**

Add the missing function fields and stub methods (panic-if-nil pattern):

```go
    sendTextFn      func(ctx context.Context, chatJID, text, replyTo string) (store.Message, error)
    sendMediaFn     func(context.Context, service.SendMediaRequest) (store.Message, error)
    editMessageFn   func(context.Context, string, string) (store.Message, error)
    deleteMessageFn func(context.Context, string) error
    sendReactionFn  func(context.Context, string, string) error
    markReadFn      func(context.Context, string) error
    sendTypingFn    func(context.Context, string, string) error
```

And the matching methods, e.g.:

```go
func (f *fakeService) SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error) {
    if f.sendTextFn == nil { panic("fakeService.SendText not stubbed") }
    return f.sendTextFn(ctx, chatJID, text, replyTo)
}
```

- [ ] **Step 4: Append tests to `tools_messages_test.go`**

```go
func TestWASendText_HappyPath(t *testing.T) {
    svc := &fakeService{
        sendTextFn: func(_ context.Context, jid, text, reply string) (store.Message, error) {
            require.Equal(t, "x@s.whatsapp.net", jid)
            require.Equal(t, "hi", text)
            require.Empty(t, reply)
            return store.Message{ID: "msg-out", Text: "hi", FromMe: true}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name: "wa_send_text",
        Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net", "text": "hi"},
    })
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWASendText_InvalidRequest(t *testing.T) {
    svc := &fakeService{
        sendTextFn: func(context.Context, string, string, string) (store.Message, error) {
            return store.Message{}, fmt.Errorf("%w: text is required", service.ErrInvalidRequest)
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_send_text",
        Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net", "text": ""},
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}

func TestWASendMedia_BadBase64(t *testing.T) {
    svc := &fakeService{
        sendMediaFn: func(context.Context, service.SendMediaRequest) (store.Message, error) {
            t.Fatal("should not call service when base64 is invalid"); return store.Message{}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name: "wa_send_media",
        Arguments: map[string]any{
            "chat_jid": "x@s.whatsapp.net", "kind": "image",
            "body_base64": "!!!not base64!!!",
        },
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}

func TestWADeleteMessage_ForbiddenMapsToToolError(t *testing.T) {
    svc := &fakeService{
        deleteMessageFn: func(context.Context, string) error {
            return fmt.Errorf("%w: not the sender", service.ErrForbidden)
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_delete_message",
        Arguments: map[string]any{"message_id": "msg-1"},
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}

func TestWAReact_HappyPath(t *testing.T) {
    svc := &fakeService{
        sendReactionFn: func(_ context.Context, id, emoji string) error {
            require.Equal(t, "msg-1", id); require.Equal(t, "👍", emoji); return nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name:      "wa_react",
        Arguments: map[string]any{"message_id": "msg-1", "emoji": "👍"},
    })
    require.NoError(t, err)
    require.False(t, res.IsError)
}
```

Add the `"fmt"` import to `tools_messages_test.go` if it isn't already there.

- [ ] **Step 5: Run, vet, commit**

```bash
go test ./internal/transport/mcp/... -count=1 -v
go vet ./internal/transport/mcp/...
```

Expected: PASS, vet clean.

```bash
git add internal/transport/mcp/tools_messages.go internal/transport/mcp/tools_messages_test.go \
        internal/transport/mcp/tools_status_test.go internal/transport/mcp/server.go
git commit -m "feat(mcp): write-side message tools — send_text/media/edit/delete/react/mark_read/typing (Plan 12 Task 5)"
```

---

## Task 6: Login + group tools

**Files:**
- Create: `internal/transport/mcp/tools_login.go` + `_test.go`
- Create: `internal/transport/mcp/tools_groups.go` + `_test.go`
- Modify: `internal/transport/mcp/tools_status_test.go` (extend `fakeService`)
- Modify: `internal/transport/mcp/server.go` (call new registrars)

**Goal:** Register `wa_login_qr`, `wa_login_phone`, `wa_logout`, `wa_create_group`, `wa_list_group_members`, `wa_update_group_members`, `wa_leave_group`.

- [ ] **Step 1: Write `tools_login.go`**

```go
package mcp

import (
    "context"
    "time"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type waLoginQROutput struct {
    QR        string    `json:"qr"`
    ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type waLoginPhoneInput struct {
    PhoneNumber string `json:"phone_number" jsonschema:"description=E.164 without the leading '+', e.g. 14155551212"`
}

type waLoginPhoneOutput struct {
    PairingCode string    `json:"pairing_code"`
    ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

func registerLoginTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_login_qr",
        Description: "Start QR pairing. Returns the first QR string emitted by the pairing channel. Render it as a QR code so the user can scan it from the WhatsApp mobile app.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waLoginQROutput, error) {
        ch, err := d.Service.LoginQR(ctx)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waLoginQROutput{}, terr
        }
        select {
        case ev := <-ch:
            return nil, waLoginQROutput{QR: ev.QR, ExpiresAt: ev.ExpiresAt}, nil
        case <-ctx.Done():
            return toolErr("login_qr: timed out waiting for QR event"), waLoginQROutput{}, nil
        }
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_login_phone",
        Description: "Start pairing-code login. Returns the 8-character code the user types on their phone.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waLoginPhoneInput) (*mcpsdk.CallToolResult, waLoginPhoneOutput, error) {
        ch, err := d.Service.LoginPhone(ctx, in.PhoneNumber)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waLoginPhoneOutput{}, terr
        }
        select {
        case ev := <-ch:
            return nil, waLoginPhoneOutput{PairingCode: ev.Code, ExpiresAt: ev.ExpiresAt}, nil
        case <-ctx.Done():
            return toolErr("login_phone: timed out waiting for pairing code"), waLoginPhoneOutput{}, nil
        }
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_logout",
        Description: "Sign the daemon out of the WhatsApp account. The user must re-pair via wa_login_qr or wa_login_phone after this.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, DestructiveHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, waOK, error) {
        if err := d.Service.Logout(ctx); err != nil {
            if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
                return res, waOK{}, terr
            }
        }
        return nil, waOK{OK: true}, nil
    })
}
```

> Reconcile field names against `waclient.QREvent` and `waclient.PairEvent` — they live in `internal/waclient/*.go`. If the QR string is on `QR` vs `Code`, use the actual name and update both the struct and the assignment.

- [ ] **Step 2: Write `tools_groups.go`**

```go
package mcp

import (
    "context"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/waclient"
)

type waCreateGroupInput struct {
    Name            string   `json:"name"`
    ParticipantJIDs []string `json:"participant_jids"`
}

type waGroupJSON struct {
    JID          string   `json:"jid"`
    Name         string   `json:"name"`
    Participants []string `json:"participants"`
}

type waCreateGroupOutput struct {
    Group waGroupJSON `json:"group"`
}

type waGroupJIDInput struct {
    GroupJID string `json:"group_jid"`
}

type waGroupMemberJSON struct {
    JID         string `json:"jid"`
    IsAdmin     bool   `json:"is_admin,omitempty"`
    IsSuperAdmin bool   `json:"is_superadmin,omitempty"`
}

type waListGroupMembersOutput struct {
    Members []waGroupMemberJSON `json:"members"`
}

type waUpdateGroupMembersInput struct {
    GroupJID        string   `json:"group_jid"`
    Action          string   `json:"action" jsonschema:"description=one of: add, remove, promote, demote"`
    ParticipantJIDs []string `json:"participant_jids"`
}

type waParticipantChangeJSON struct {
    JID    string `json:"jid"`
    Code   int    `json:"code,omitempty"`
    Error  string `json:"error,omitempty"`
}

type waUpdateGroupMembersOutput struct {
    Changes []waParticipantChangeJSON `json:"changes"`
}

func registerGroupTools(srv *mcpsdk.Server, d Deps) {
    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_create_group",
        Description: "Create a new WhatsApp group with the given name and initial participants (must include at least one besides yourself).",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waCreateGroupInput) (*mcpsdk.CallToolResult, waCreateGroupOutput, error) {
        g, err := d.Service.CreateGroup(ctx, in.Name, in.ParticipantJIDs)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waCreateGroupOutput{}, terr
        }
        return nil, waCreateGroupOutput{Group: groupToJSON(g)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_list_group_members",
        Description: "List members of a group. Read-only.",
        Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGroupJIDInput) (*mcpsdk.CallToolResult, waListGroupMembersOutput, error) {
        rows, err := d.Service.ListGroupMembers(ctx, in.GroupJID)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waListGroupMembersOutput{}, terr
        }
        return nil, waListGroupMembersOutput{Members: groupMembersToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_update_group_members",
        Description: "Add, remove, promote, or demote group participants. Returns a per-participant change result with WhatsApp error codes where applicable.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waUpdateGroupMembersInput) (*mcpsdk.CallToolResult, waUpdateGroupMembersOutput, error) {
        rows, err := d.Service.UpdateGroupMembers(ctx, in.GroupJID, in.Action, in.ParticipantJIDs)
        if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
            return res, waUpdateGroupMembersOutput{}, terr
        }
        return nil, waUpdateGroupMembersOutput{Changes: participantChangesToJSON(rows)}, nil
    })

    mcpsdk.AddTool(srv, &mcpsdk.Tool{
        Name:        "wa_leave_group",
        Description: "Leave a group. Destructive — the daemon will no longer receive messages from this group.",
        Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: true, DestructiveHint: true},
    }, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGroupJIDInput) (*mcpsdk.CallToolResult, waOK, error) {
        if err := d.Service.LeaveGroup(ctx, in.GroupJID); err != nil {
            if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
                return res, waOK{}, terr
            }
        }
        return nil, waOK{OK: true}, nil
    })
}

func groupToJSON(g waclient.Group) waGroupJSON {
    parts := make([]string, 0, len(g.Participants))
    for _, p := range g.Participants {
        parts = append(parts, p.JID)
    }
    return waGroupJSON{JID: g.JID, Name: g.Name, Participants: parts}
}
func groupMembersToJSON(rows []waclient.GroupMember) []waGroupMemberJSON {
    out := make([]waGroupMemberJSON, 0, len(rows))
    for _, m := range rows {
        out = append(out, waGroupMemberJSON{JID: m.JID, IsAdmin: m.IsAdmin, IsSuperAdmin: m.IsSuperAdmin})
    }
    return out
}
func participantChangesToJSON(rows []waclient.ParticipantChange) []waParticipantChangeJSON {
    out := make([]waParticipantChangeJSON, 0, len(rows))
    for _, c := range rows {
        out = append(out, waParticipantChangeJSON{JID: c.JID, Code: c.Code, Error: c.Error})
    }
    return out
}
```

> Reconcile field names with `internal/waclient/groups.go` — `waclient.Group`, `waclient.GroupMember`, `waclient.ParticipantChange`. Field names may differ; mirror what the REST groups handler emits.

- [ ] **Step 3: Register the new tool groups**

In `internal/transport/mcp/server.go`:

```go
    registerLoginTools(srv, d)
    registerGroupTools(srv, d)
```

- [ ] **Step 4: Extend `fakeService` (and write the tests)**

Add to the struct in `tools_status_test.go`:

```go
    loginQRFn            func(context.Context) (<-chan waclient.QREvent, error)
    loginPhoneFn         func(context.Context, string) (<-chan waclient.PairEvent, error)
    logoutFn             func(context.Context) error
    createGroupFn        func(context.Context, string, []string) (waclient.Group, error)
    listGroupMembersFn   func(context.Context, string) ([]waclient.GroupMember, error)
    updateGroupMembersFn func(context.Context, string, string, []string) ([]waclient.ParticipantChange, error)
    leaveGroupFn         func(context.Context, string) error
```

Methods follow the same panic-if-nil shape.

Write `tools_login_test.go`:

```go
package mcp

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/askarzh/whatsmeow-api/internal/waclient"
)

func TestWALoginQR_ReturnsFirstEvent(t *testing.T) {
    ch := make(chan waclient.QREvent, 1)
    ch <- waclient.QREvent{QR: "qr-string", ExpiresAt: time.Unix(123, 0)}
    svc := &fakeService{
        loginQRFn: func(context.Context) (<-chan waclient.QREvent, error) { return ch, nil },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_login_qr"})
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWALogout_HappyPath(t *testing.T) {
    svc := &fakeService{logoutFn: func(context.Context) error { return nil }}
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_logout"})
    require.NoError(t, err)
    require.False(t, res.IsError)
}
```

Write `tools_groups_test.go`:

```go
package mcp

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/askarzh/whatsmeow-api/internal/waclient"
)

func TestWACreateGroup_HappyPath(t *testing.T) {
    svc := &fakeService{
        createGroupFn: func(_ context.Context, name string, jids []string) (waclient.Group, error) {
            require.Equal(t, "Test", name)
            require.Equal(t, []string{"x@s.whatsapp.net"}, jids)
            return waclient.Group{JID: "g@g.us", Name: name}, nil
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name: "wa_create_group",
        Arguments: map[string]any{
            "name":             "Test",
            "participant_jids": []any{"x@s.whatsapp.net"},
        },
    })
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestWAUpdateGroupMembers_RejectsInvalidAction(t *testing.T) {
    svc := &fakeService{
        updateGroupMembersFn: func(_ context.Context, _, action string, _ []string) ([]waclient.ParticipantChange, error) {
            return nil, fmt.Errorf("%w: invalid action %q", service.ErrInvalidRequest, action)
        },
    }
    ctx, session := inMemoryClient(t, svc)
    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
        Name: "wa_update_group_members",
        Arguments: map[string]any{
            "group_jid":        "g@g.us",
            "action":           "bogus",
            "participant_jids": []any{"x@s.whatsapp.net"},
        },
    })
    require.NoError(t, err)
    require.True(t, res.IsError)
}
```

Add `"fmt"` and `"github.com/askarzh/whatsmeow-api/internal/service"` imports to `tools_groups_test.go`.

- [ ] **Step 5: Run, vet, commit**

```bash
go test ./internal/transport/mcp/... -count=1 -v
go vet ./internal/transport/mcp/...
```

Expected: PASS, vet clean.

```bash
git add internal/transport/mcp/tools_login.go internal/transport/mcp/tools_login_test.go \
        internal/transport/mcp/tools_groups.go internal/transport/mcp/tools_groups_test.go \
        internal/transport/mcp/tools_status_test.go internal/transport/mcp/server.go
git commit -m "feat(mcp): login + group tools (Plan 12 Task 6)"
```

---

## Task 7: ListTools contract + instructions contract

**Files:**
- Create: `internal/transport/mcp/contract_test.go`

> Why a new file and not extending `server_test.go`? The smoke test from Task 1 lives in `package mcp_test` (black-box) and can only see exported names. The contract tests below use `inMemoryClient` and `newServer`, which are unexported. They must live in `package mcp` alongside `tools_status_test.go`.

**Goal:** Lock the public contract of the MCP server with two assertions: (a) the initialize response advertises the 25 expected tool names; (b) the `Instructions` field is non-empty and under 1 KB.

- [ ] **Step 1: Create `contract_test.go` (package mcp, white-box)**

```go
package mcp

import (
    "testing"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/stretchr/testify/require"
)

func TestListTools_AllNamesPresent(t *testing.T) {
    svc := &fakeService{}
    ctx, session := inMemoryClient(t, svc)

    res, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
    require.NoError(t, err)

    got := make(map[string]bool, len(res.Tools))
    for _, tool := range res.Tools {
        got[tool.Name] = true
    }

    want := []string{
        "wa_status", "wa_stats",
        "wa_login_qr", "wa_login_phone", "wa_logout",
        "wa_send_text", "wa_send_media", "wa_get_media",
        "wa_edit_message", "wa_delete_message",
        "wa_react", "wa_list_reactions",
        "wa_mark_read", "wa_typing", "wa_list_receipts",
        "wa_list_chats", "wa_get_chat", "wa_list_messages", "wa_search_messages",
        "wa_list_contacts", "wa_search_contacts",
        "wa_create_group", "wa_list_group_members", "wa_update_group_members", "wa_leave_group",
    }
    require.Len(t, want, 25, "Plan 12 spec promised exactly 25 tools")
    for _, name := range want {
        require.True(t, got[name], "tool %q is missing from ListTools response", name)
    }
    require.Equal(t, len(want), len(res.Tools), "ListTools returned unexpected extras: %v", res.Tools)
}

func TestServerInstructions_NonEmptyAndShort(t *testing.T) {
    svc := &fakeService{}
    ctx, session := inMemoryClient(t, svc)

    // The instructions live in the initialize response; mcp.Client surfaces
    // them on the session after Connect. Use whichever accessor the SDK exposes.
    instr := session.InitializeResult().Instructions
    require.NotEmpty(t, instr, "server instructions must be present so hosts can prime Claude")
    require.LessOrEqual(t, len(instr), 1024, "instructions exceed 1 KB; tighten the prose")
}
```

> If `session.ListTools` or `session.InitializeResult()` have different names in the pinned SDK, adapt to whatever the SDK exposes. Both pieces of info — the tool list and the instructions — are first-class MCP concepts so equivalents exist.

- [ ] **Step 2: Run, commit**

```bash
go test ./internal/transport/mcp/... -count=1 -v
```

Expected: both new tests PASS. If `TestListTools_AllNamesPresent` fails, the registrar chain in `server.go` is missing a tool — add it.

```bash
git add internal/transport/mcp/contract_test.go
git commit -m "test(mcp): assert 25-tool catalog + instructions contract (Plan 12 Task 7)"
```

---

## Task 8: Streamable-HTTP end-to-end test

**Files:**
- Create: `internal/transport/mcp/integration_test.go`
- Create: `internal/transport/mcp/integration_help_test.go`

**Goal:** Prove that the full chain — chi router + bearer middleware + streamable-HTTP handler + MCP server — works against a real `mcp.Client` over `httptest.Server`. Catches breakage that in-memory transports miss (auth, content-type negotiation, session-id handling).

- [ ] **Step 1: Write `integration_help_test.go` (the full `service.Service` boilerplate)**

The integration tests live in `package mcp_test` (black-box) so they can't see the white-box `fakeService`. This helper file provides a complete `service.Service` implementation — `integMinService` — with only `Status` returning real data and every other method panicking with a clear message.

```go
package mcp_test

import (
    "context"
    "time"

    "github.com/askarzh/whatsmeow-api/internal/service"
    "github.com/askarzh/whatsmeow-api/internal/store"
    "github.com/askarzh/whatsmeow-api/internal/waclient"
)

type integMinService struct{}

var _ service.Service = integMinService{}

func (integMinService) Status(_ context.Context) (waclient.Status, error) {
    return waclient.Status{State: waclient.StateConnected, JID: "1@s.whatsapp.net"}, nil
}

func (integMinService) LoginQR(context.Context) (<-chan waclient.QREvent, error)            { panic("integMinService.LoginQR not stubbed") }
func (integMinService) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { panic("integMinService.LoginPhone not stubbed") }
func (integMinService) Logout(context.Context) error                                         { panic("integMinService.Logout not stubbed") }

func (integMinService) SendText(context.Context, string, string, string) (store.Message, error) {
    panic("integMinService.SendText not stubbed")
}

func (integMinService) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
    panic("integMinService.ListChats not stubbed")
}
func (integMinService) GetChat(context.Context, string) (store.Chat, error) { panic("integMinService.GetChat not stubbed") }
func (integMinService) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
    panic("integMinService.ListMessages not stubbed")
}
func (integMinService) SearchMessages(context.Context, string, int) ([]store.Message, error) {
    panic("integMinService.SearchMessages not stubbed")
}
func (integMinService) ListContacts(context.Context) ([]store.Contact, error) { panic("integMinService.ListContacts not stubbed") }
func (integMinService) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
    panic("integMinService.SearchContacts not stubbed")
}
func (integMinService) Stats(context.Context) (service.Stats, error) { panic("integMinService.Stats not stubbed") }

func (integMinService) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
    panic("integMinService.SendMedia not stubbed")
}
func (integMinService) GetMediaRef(context.Context, string) (store.MediaRef, error) {
    panic("integMinService.GetMediaRef not stubbed")
}

func (integMinService) EditMessage(context.Context, string, string) (store.Message, error) {
    panic("integMinService.EditMessage not stubbed")
}
func (integMinService) DeleteMessage(context.Context, string) error { panic("integMinService.DeleteMessage not stubbed") }

func (integMinService) SendReaction(context.Context, string, string) error { panic("integMinService.SendReaction not stubbed") }
func (integMinService) ListReactions(context.Context, string) ([]store.Reaction, error) {
    panic("integMinService.ListReactions not stubbed")
}

func (integMinService) MarkMessageRead(context.Context, string) error  { panic("integMinService.MarkMessageRead not stubbed") }
func (integMinService) SendTyping(context.Context, string, string) error { panic("integMinService.SendTyping not stubbed") }
func (integMinService) ListReceipts(context.Context, string) ([]store.Receipt, error) {
    panic("integMinService.ListReceipts not stubbed")
}

func (integMinService) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
    panic("integMinService.CreateGroup not stubbed")
}
func (integMinService) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
    panic("integMinService.ListGroupMembers not stubbed")
}
func (integMinService) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
    panic("integMinService.UpdateGroupMembers not stubbed")
}
func (integMinService) LeaveGroup(context.Context, string) error { panic("integMinService.LeaveGroup not stubbed") }
```

> If the Service interface gained a method since this plan was written, the compiler will tell you — the `var _ service.Service = integMinService{}` line is a build-time conformance check. Add a panicking stub for the missing method.

- [ ] **Step 2: Write `integration_test.go`**

```go
package mcp_test

import (
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/stretchr/testify/require"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/askarzh/whatsmeow-api/internal/config"
    "github.com/askarzh/whatsmeow-api/internal/store"
    httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
)

func TestStreamableHTTP_EndToEnd_HappyPath(t *testing.T) {
    router := httpapi.NewRouter(httpapi.Deps{
        Config: config.Config{
            Auth: config.AuthConfig{Token: "tok"},
            MCP:  config.MCPConfig{Enabled: true},
        },
        Service: integMinService{},
        Store:   store.Bundle{},
    })
    ts := httptest.NewServer(router)
    t.Cleanup(ts.Close)

    client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "it-test", Version: "0"}, nil)
    transport := mcpsdk.NewStreamableClientTransport(ts.URL+"/v1/mcp", &mcpsdk.StreamableClientTransportOptions{
        HTTPClient: &http.Client{
            Transport: bearerRoundTripper{base: http.DefaultTransport, token: "tok"},
        },
    })

    ctx := context.Background()
    session, err := client.Connect(ctx, transport, nil)
    require.NoError(t, err)
    t.Cleanup(func() { _ = session.Close() })

    res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "wa_status"})
    require.NoError(t, err)
    require.False(t, res.IsError)
}

func TestStreamableHTTP_MissingBearer_401(t *testing.T) {
    router := httpapi.NewRouter(httpapi.Deps{
        Config:  config.Config{Auth: config.AuthConfig{Token: "tok"}, MCP: config.MCPConfig{Enabled: true}},
        Service: integMinService{},
    })
    ts := httptest.NewServer(router)
    t.Cleanup(ts.Close)

    req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/mcp", strings.NewReader(`{}`))
    req.Header.Set("Content-Type", "application/json")
    resp, err := http.DefaultClient.Do(req)
    require.NoError(t, err)
    require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// bearerRoundTripper attaches Authorization: Bearer <token> to every request.
type bearerRoundTripper struct {
    base  http.RoundTripper
    token string
}

func (b bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    req.Header.Set("Authorization", "Bearer "+b.token)
    return b.base.RoundTrip(req)
}
```

> The exact name `mcp.NewStreamableClientTransport` may differ in the pinned SDK (other candidates: `mcp.NewStreamableHTTPClientTransport`, `mcp.NewClientStreamableTransport`). Use whatever `go doc github.com/modelcontextprotocol/go-sdk/mcp | grep -i streamable.*client` reveals.

- [ ] **Step 3: Run the integration tests**

```bash
go test ./internal/transport/mcp/... -count=1 -run TestStreamableHTTP -v
```

Expected: both tests PASS.

- [ ] **Step 4: Run the full suite**

```bash
go test ./... -count=1
```

Expected: PASS across the repo.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/mcp/integration_test.go internal/transport/mcp/integration_help_test.go
git commit -m "test(mcp): streamable-HTTP end-to-end happy path + auth (Plan 12 Task 8)"
```

---

## Task 9: Docs — README, example, master design doc

**Files:**
- Create: `examples/claude-mcp/README.md`
- Create: `examples/claude-mcp/claude_code_config.json`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md`

**Goal:** A new user can connect Claude Code (or claude.ai via tunnel) to the daemon in three commands.

- [ ] **Step 1: Write `examples/claude-mcp/claude_code_config.json`**

```json
{
  "mcpServers": {
    "whatsmeow-api": {
      "type": "http",
      "url": "http://localhost:8080/v1/mcp",
      "headers": {
        "Authorization": "Bearer ${WMAPI_AUTH_TOKEN}"
      }
    }
  }
}
```

> The exact JSON shape Claude Code expects is documented at https://docs.claude.com/en/docs/claude-code/mcp; if the field names there differ, mirror them.

- [ ] **Step 2: Write `examples/claude-mcp/README.md`**

```markdown
# Connect Claude to whatsmeow-api

Three steps.

## 1. Start the daemon

Either way, the daemon must bind a port reachable from your MCP host. Localhost is enough for Claude Code; for Claude Desktop or claude.ai, expose it via a tunnel (cloudflared, ngrok) or a reverse proxy.

```sh
docker run --rm \
  -p 8080:8080 \
  -e WMAPI_AUTH__TOKEN=$(openssl rand -hex 16) \
  -v wm-data:/data \
  ghcr.io/askarzh/whatsmeow-api:latest
```

Note the token — you'll need it below.

## 2. Pair (one-time)

If you haven't already, sign in via `wa_login_qr` or `wa_login_phone` once the MCP server is connected. The REST cookbook (`examples/cookbook.md`) covers the same flow.

## 3. Add the MCP server to Claude

### Claude Code

Drop `claude_code_config.json` into your project's `.mcp.json` (or merge with an existing file), and replace `${WMAPI_AUTH_TOKEN}` with the token from step 1. Re-open the project.

### Claude Desktop / claude.ai

Settings → Connectors → Add custom connector → paste the daemon's public URL (e.g. `https://your-domain/v1/mcp`) and the bearer token.

## 4. Try it

Ask Claude:
> "What's my WhatsApp status?"

Claude calls `wa_status` and reports back.
```

- [ ] **Step 3: Update `README.md`**

Insert a new section after the "Run with Docker" section:

```markdown
## Connect from Claude

The daemon speaks MCP over streamable HTTP at `/v1/mcp`. Claude Code, Claude Desktop, and claude.ai can drive every capability through 25 typed tools (`wa_status`, `wa_send_text`, `wa_list_chats`, …).

See [`examples/claude-mcp/`](examples/claude-mcp/) for a copy-pasteable setup. The full tool catalog lives in [Plan 12's spec](docs/superpowers/specs/2026-05-13-whatsmeow-api-12-mcp-server-design.md).
```

Add a roadmap entry:

```markdown
- ✅ **Plan 12 — MCP server transport** (2026-05-13): MCP-over-HTTP on `/v1/mcp`, 25 tools, reuses bearer auth.
```

If the README has a "v1 complete" line, update it to "v1 + MCP shipped" or similar — preserve the existing wording style.

- [ ] **Step 4: Touch up the master design doc**

Open `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md`. Find each occurrence of "separate MCP server" (case-insensitive, the phrase appears in §0 and §1 — verify with `grep -n "separate MCP server" docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md`) and rewrite to "MCP-over-HTTP transport on the daemon" or equivalent. Keep the sentence structure; only swap the noun phrase.

- [ ] **Step 5: Final verification**

```bash
go test ./... -count=1
go vet ./...
```

Expected: green.

- [ ] **Step 6: Commit**

```bash
git add examples/claude-mcp/ README.md docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md
git commit -m "docs(mcp): README section, claude-mcp example, master design doc cleanup (Plan 12 Task 9)"
```

---

## Final verification

Run from a clean tree:

```bash
go mod tidy
go build ./...
go test ./... -race -count=1
go vet ./...
```

All green. The local daemon at `http://localhost:8080/v1/mcp` is callable from Claude Code as soon as a bearer token is set.

## Out-of-scope reminders (do not implement)

- MCP-over-stdio shim, server-initiated notifications, elicitation, MCP app widgets, `file://` / `https://` media sources, connector-directory submission, per-tool audit log/metrics. Listed in §13 of the spec.

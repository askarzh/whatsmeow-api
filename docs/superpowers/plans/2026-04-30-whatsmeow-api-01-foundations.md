# whatsmeow-api Plan 01 — Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Boot a minimal whatsmeow-api daemon that loads typed config from TOML+env, logs structured output, serves `/v1/health` and a placeholder `/v1/status`, and enforces a bearer-token auth fail-safe — laying the groundwork for every later plan.

**Architecture:** A single Go binary with a `cobra` root command and a `serve` subcommand. Configuration is parsed by `koanf` (TOML file + `WMAPI_*` env overrides) into a typed `config.Config` struct. The HTTP layer uses `chi/v5` with auth + recovery + request-id middleware. Errors over the wire are RFC 7807 `application/problem+json`. The server enforces "no token + non-localhost bind" as a startup error.

**Tech Stack:**
- Go 1.26
- `github.com/spf13/cobra` — CLI subcommands
- `github.com/knadh/koanf/v2` (+ `parsers/toml/v2`, `providers/file`, `providers/env/v2`, `providers/confmap`) — config
- `github.com/go-chi/chi/v5` — HTTP router
- `github.com/stretchr/testify` — test assertions
- `log/slog` (stdlib) — structured logging

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go` | `Config` struct, defaults, `Load(path string)`, `Validate()`. |
| `internal/config/config_test.go` | Defaults, TOML parsing, env override, validation tests. |
| `internal/logging/logging.go` | `New(cfg)` returns a `*slog.Logger` (text or JSON). |
| `internal/logging/logging_test.go` | Format selection + level test. |
| `internal/transport/http/problem.go` | `Problem` type + `Write(w, status, code, detail)` helper. |
| `internal/transport/http/problem_test.go` | Round-trip test. |
| `internal/transport/http/auth.go` | Bearer-token middleware. |
| `internal/transport/http/auth_test.go` | Pass / no-header / wrong-token / disabled cases. |
| `internal/transport/http/health.go` | `Health` handler (always OK for now). |
| `internal/transport/http/health_test.go` | 200 + payload shape. |
| `internal/transport/http/status.go` | `Status` handler returning placeholder connection state. |
| `internal/transport/http/status_test.go` | 200 + payload shape. |
| `internal/transport/http/router.go` | `NewRouter(deps)` wires middleware + routes. |
| `internal/transport/http/router_test.go` | Auth middleware applied to `/v1/status` but not `/v1/health`. |
| `internal/transport/http/server.go` | `Server` struct: bootstrap, `Run(ctx)` with graceful shutdown. |
| `internal/transport/http/server_test.go` | Server starts, serves `/v1/health`, shuts down on ctx cancel. |
| `cmd/whatsmeow-api/main.go` | Root cobra command (replaces current stub). |
| `cmd/whatsmeow-api/serve.go` | `serve` subcommand: load config → logger → server → run. |
| `README.md` | Add quick-start with the new commands. |

Tests live next to the code they cover (Go convention: `_test.go` in same package).

---

## Task 1: Add Go dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the runtime dependencies**

Run:
```bash
cd /home/askar/src/whatsmeow-api
go get github.com/spf13/cobra@latest
go get github.com/go-chi/chi/v5@latest
go get github.com/knadh/koanf/v2@latest
go get github.com/knadh/koanf/parsers/toml/v2@latest
go get github.com/knadh/koanf/providers/file@latest
go get github.com/knadh/koanf/providers/env/v2@latest
go get github.com/knadh/koanf/providers/confmap@latest
```

- [ ] **Step 2: Add the test dependency**

Run:
```bash
go get github.com/stretchr/testify@latest
```

- [ ] **Step 3: Tidy and verify**

Run:
```bash
go mod tidy
go build ./...
```
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add cobra, chi, koanf, testify"
```

---

## Task 2: Config struct with defaults

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config_test

import (
	"testing"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	c, err := config.Load("")
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", c.Server.Bind)
	assert.Equal(t, 8080, c.Server.Port)
	assert.Equal(t, "", c.Auth.Token)
	assert.Equal(t, "sqlite", c.Storage.Backend)
	assert.Equal(t, "./data/whatsmeow-api.db", c.Storage.SQLitePath)
	assert.Equal(t, "", c.Storage.PostgresDSN)
	assert.Equal(t, "./data", c.DataDir)
	assert.Equal(t, "info", c.Log.Level)
	assert.Equal(t, "text", c.Log.Format)
	assert.Equal(t, 24, c.Events.RetentionHours)
	assert.False(t, c.Metrics.Enabled)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL, package does not compile (config does not exist).

- [ ] **Step 3: Write the implementation**

Create `internal/config/config.go`:
```go
// Package config loads daemon configuration from TOML files and WMAPI_* env vars.
package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	envprov "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	DataDir string        `koanf:"data_dir"`
	Server  ServerConfig  `koanf:"server"`
	Auth    AuthConfig    `koanf:"auth"`
	Storage StorageConfig `koanf:"storage"`
	Log     LogConfig     `koanf:"log"`
	Events  EventsConfig  `koanf:"events"`
	Metrics MetricsConfig `koanf:"metrics"`
}

type ServerConfig struct {
	Bind string `koanf:"bind"`
	Port int    `koanf:"port"`
}

type AuthConfig struct {
	Token string `koanf:"token"`
}

type StorageConfig struct {
	Backend     string `koanf:"backend"`
	SQLitePath  string `koanf:"sqlite_path"`
	PostgresDSN string `koanf:"postgres_dsn"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type EventsConfig struct {
	RetentionHours int `koanf:"retention_hours"`
}

type MetricsConfig struct {
	Enabled bool `koanf:"enabled"`
}

func defaults() map[string]any {
	return map[string]any{
		"data_dir":                "./data",
		"server.bind":             "127.0.0.1",
		"server.port":             8080,
		"auth.token":              "",
		"storage.backend":         "sqlite",
		"storage.sqlite_path":     "./data/whatsmeow-api.db",
		"storage.postgres_dsn":    "",
		"log.level":               "info",
		"log.format":              "text",
		"events.retention_hours":  24,
		"metrics.enabled":         false,
	}
}

// Load reads configuration with the precedence: env > file > defaults.
// Pass "" for path to skip the file source.
func Load(path string) (Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	if path != "" {
		if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load file %q: %w", path, err)
		}
	}

	envP := envprov.Provider(".", envprov.Opt{
		Prefix: "WMAPI_",
		TransformFunc: func(key, value string) (string, any) {
			key = strings.ToLower(strings.TrimPrefix(key, "WMAPI_"))
			key = strings.ReplaceAll(key, "__", ".")
			return key, value
		},
	})
	if err := k.Load(envP, nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	var c Config
	if err := k.Unmarshal("", &c); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}
	return c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/...`
Expected: `PASS`, `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: typed Config struct with defaults"
```

---

## Task 3: Config TOML file loading

**Files:**
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/config/config_test.go`:
```go
func TestLoadFromTOML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.toml"
	require.NoError(t, os.WriteFile(path, []byte(`
data_dir = "/tmp/wm"
[server]
bind = "0.0.0.0"
port = 9000
[auth]
token = "secret"
[storage]
backend = "postgres"
postgres_dsn = "postgres://x"
[log]
level = "debug"
format = "json"
`), 0o600))

	c, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "/tmp/wm", c.DataDir)
	assert.Equal(t, "0.0.0.0", c.Server.Bind)
	assert.Equal(t, 9000, c.Server.Port)
	assert.Equal(t, "secret", c.Auth.Token)
	assert.Equal(t, "postgres", c.Storage.Backend)
	assert.Equal(t, "postgres://x", c.Storage.PostgresDSN)
	assert.Equal(t, "debug", c.Log.Level)
	assert.Equal(t, "json", c.Log.Format)
}
```

Add to the imports block at the top of the same file:
```go
import (
	"os"
	"testing"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/config/... -run TestLoadFromTOML`
Expected: `PASS` (the implementation from Task 2 already supports it).

- [ ] **Step 3: Commit**

```bash
git add internal/config/config_test.go
git commit -m "config: test TOML file loading"
```

---

## Task 4: Config env-variable override

**Files:**
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/config/config_test.go`:
```go
func TestEnvOverride(t *testing.T) {
	t.Setenv("WMAPI_SERVER__PORT", "12345")
	t.Setenv("WMAPI_AUTH__TOKEN", "from-env")
	t.Setenv("WMAPI_LOG__FORMAT", "json")

	c, err := config.Load("")
	require.NoError(t, err)

	assert.Equal(t, 12345, c.Server.Port)
	assert.Equal(t, "from-env", c.Auth.Token)
	assert.Equal(t, "json", c.Log.Format)
}
```

The double-underscore convention `WMAPI_SECTION__KEY` maps to `section.key`, since single underscores can appear inside key names (`sqlite_path`).

- [ ] **Step 2: Run the test**

Run: `go test ./internal/config/... -run TestEnvOverride`
Expected: `PASS`.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config_test.go
git commit -m "config: test env-var override"
```

---

## Task 5: Config validation with bind/auth fail-safe

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/config/config_test.go`:
```go
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:    "ok defaults",
			mutate:  func(c *config.Config) {},
			wantErr: "",
		},
		{
			name:    "non-localhost bind without token rejected",
			mutate:  func(c *config.Config) { c.Server.Bind = "0.0.0.0" },
			wantErr: "auth.token is required when server.bind is not 127.0.0.1",
		},
		{
			name:    "non-localhost bind with token allowed",
			mutate: func(c *config.Config) {
				c.Server.Bind = "0.0.0.0"
				c.Auth.Token = "x"
			},
			wantErr: "",
		},
		{
			name:    "unknown storage backend rejected",
			mutate:  func(c *config.Config) { c.Storage.Backend = "redis" },
			wantErr: `storage.backend must be "sqlite" or "postgres"`,
		},
		{
			name:    "postgres backend requires DSN",
			mutate:  func(c *config.Config) { c.Storage.Backend = "postgres" },
			wantErr: `storage.postgres_dsn is required when storage.backend is "postgres"`,
		},
		{
			name:    "invalid log level rejected",
			mutate:  func(c *config.Config) { c.Log.Level = "trace" },
			wantErr: `log.level must be one of debug, info, warn, error`,
		},
		{
			name:    "invalid log format rejected",
			mutate:  func(c *config.Config) { c.Log.Format = "xml" },
			wantErr: `log.format must be "text" or "json"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load("")
			require.NoError(t, err)
			tc.mutate(&c)
			err = c.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestValidate`
Expected: FAIL, `Validate undefined`.

- [ ] **Step 3: Add the Validate method**

First, update the import block at the top of `internal/config/config.go` to add `"errors"`. The full updated import block is:
```go
import (
	"errors"
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	envprov "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)
```

Then append this method to the bottom of `internal/config/config.go`:
```go
func (c Config) Validate() error {
	if c.Server.Bind != "127.0.0.1" && c.Server.Bind != "::1" && c.Auth.Token == "" {
		return errors.New("auth.token is required when server.bind is not 127.0.0.1")
	}
	switch c.Storage.Backend {
	case "sqlite":
		// ok
	case "postgres":
		if c.Storage.PostgresDSN == "" {
			return errors.New(`storage.postgres_dsn is required when storage.backend is "postgres"`)
		}
	default:
		return errors.New(`storage.backend must be "sqlite" or "postgres"`)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return errors.New(`log.level must be one of debug, info, warn, error`)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return errors.New(`log.format must be "text" or "json"`)
	}
	return nil
}
```

(The `import "errors"` line above is illustrative — actually merge `"errors"` into the existing import block at the top of the file so there is only one import block.)

- [ ] **Step 4: Run the test**

Run: `go test ./internal/config/...`
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: validation with localhost-bind fail-safe"
```

---

## Task 6: Logging setup

**Files:**
- Create: `internal/logging/logging.go`
- Test: `internal/logging/logging_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/logging/logging_test.go`:
```go
package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/askar/whatsmeow-api/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJSON(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "debug", Format: "json"}, &buf)
	require.NoError(t, err)

	logger.Info("hello", slog.String("k", "v"))

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "hello", rec["msg"])
	assert.Equal(t, "v", rec["k"])
}

func TestNewText(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "info", Format: "text"}, &buf)
	require.NoError(t, err)

	logger.Info("hello")
	assert.Contains(t, buf.String(), "hello")
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(config.LogConfig{Level: "warn", Format: "text"}, &buf)
	require.NoError(t, err)

	logger.Info("ignored")
	logger.Warn("kept")

	out := buf.String()
	assert.NotContains(t, out, "ignored")
	assert.True(t, strings.Contains(out, "kept"))
}

func TestRejectsUnknownFormat(t *testing.T) {
	_, err := logging.New(config.LogConfig{Level: "info", Format: "xml"}, &bytes.Buffer{})
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logging/...`
Expected: FAIL, package missing.

- [ ] **Step 3: Write the implementation**

Create `internal/logging/logging.go`:
```go
// Package logging builds a *slog.Logger from the daemon's log config.
package logging

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/askar/whatsmeow-api/internal/config"
)

func New(cfg config.LogConfig, out io.Writer) (*slog.Logger, error) {
	var lvl slog.Level
	switch cfg.Level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", cfg.Level)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch cfg.Format {
	case "json":
		h = slog.NewJSONHandler(out, opts)
	case "text":
		h = slog.NewTextHandler(out, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q", cfg.Format)
	}
	return slog.New(h), nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/logging/...`
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/logging/logging.go internal/logging/logging_test.go
git commit -m "logging: slog logger from typed config"
```

---

## Task 7: problem+json error helper

**Files:**
- Create: `internal/transport/http/problem.go`
- Test: `internal/transport/http/problem_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/problem_test.go`:
```go
package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProblemWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteProblem(rr, http.StatusForbidden, "auth.unauthorized", "missing token")

	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
	assert.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))

	var p httpapi.Problem
	require.NoError(t, json.NewDecoder(res.Body).Decode(&p))
	assert.Equal(t, "auth.unauthorized", p.Code)
	assert.Equal(t, "missing token", p.Detail)
	assert.Equal(t, http.StatusForbidden, p.Status)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/...`
Expected: FAIL, missing symbols.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/problem.go`:
```go
// Package http exposes the service layer as a JSON HTTP API using chi.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func WriteProblem(w http.ResponseWriter, status int, code, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
		Detail: detail,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Default().Error("write problem", "err", err)
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/transport/http/... -run TestProblemWrite`
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/problem.go internal/transport/http/problem_test.go
git commit -m "http: RFC 7807 problem+json helper"
```

---

## Task 8: /v1/health handler

**Files:**
- Create: `internal/transport/http/health.go`
- Test: `internal/transport/http/health_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/health_test.go`:
```go
package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)

	httpapi.HealthHandler().ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
	_, hasDB := body["db"]
	_, hasWA := body["wa_connected"]
	assert.True(t, hasDB, "db field present")
	assert.True(t, hasWA, "wa_connected field present")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/... -run TestHealthHandler`
Expected: FAIL, `HealthHandler undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/health.go`:
```go
package http

import (
	"encoding/json"
	"net/http"
)

// HealthHandler returns liveness. db / wa_connected are nil until the
// later plans wire real probes.
func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"ok":           true,
			"db":           nil,
			"wa_connected": nil,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/transport/http/... -run TestHealthHandler`
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/health.go internal/transport/http/health_test.go
git commit -m "http: /v1/health handler"
```

---

## Task 9: /v1/status placeholder handler

**Files:**
- Create: `internal/transport/http/status.go`
- Test: `internal/transport/http/status_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/status_test.go`:
```go
package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusHandlerPlaceholder(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)

	httpapi.StatusHandler().ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, false, body["wa_connected"])
	assert.Equal(t, nil, body["jid"])
	assert.Equal(t, nil, body["since"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/... -run TestStatusHandlerPlaceholder`
Expected: FAIL, `StatusHandler undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/status.go`:
```go
package http

import (
	"encoding/json"
	"net/http"
)

// StatusHandler returns the WhatsApp connection state. Until Plan 02
// wires the waclient, it is a placeholder reporting "not connected".
func StatusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"wa_connected": false,
			"jid":          nil,
			"since":        nil,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/transport/http/... -run TestStatusHandlerPlaceholder`
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/status.go internal/transport/http/status_test.go
git commit -m "http: /v1/status placeholder handler"
```

---

## Task 10: Bearer-token auth middleware

**Files:**
- Create: `internal/transport/http/auth.go`
- Test: `internal/transport/http/auth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/auth_test.go`:
```go
package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthDisabledLetsThrough(t *testing.T) {
	mw := httpapi.RequireBearerToken("")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthEnabledMissingHeader(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestAuthEnabledWrongToken(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer nope")
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthEnabledRightToken(t *testing.T) {
	mw := httpapi.RequireBearerToken("s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	mw(okHandler()).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/... -run TestAuth`
Expected: FAIL, `RequireBearerToken undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/auth.go`:
```go
package http

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearerToken returns middleware that checks an Authorization: Bearer
// header. When token is empty the middleware is a no-op (auth disabled).
func RequireBearerToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "missing bearer token")
				return
			}
			got := h[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/transport/http/... -run TestAuth`
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/auth.go internal/transport/http/auth_test.go
git commit -m "http: bearer-token auth middleware"
```

---

## Task 11: Router that wires middleware and routes

**Files:**
- Create: `internal/transport/http/router.go`
- Test: `internal/transport/http/router_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/router_test.go`:
```go
package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/askar/whatsmeow-api/internal/config"
	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
)

func TestRouterHealthIsPublic(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{Auth: config.AuthConfig{Token: "s3cret"}}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRouterStatusRequiresAuth(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{Auth: config.AuthConfig{Token: "s3cret"}}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRouterAuthDisabledStatusOpen(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Deps{Config: config.Config{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/... -run TestRouter`
Expected: FAIL, `NewRouter undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/router.go`:
```go
package http

import (
	"log/slog"
	"net/http"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps is the bundle of values the router depends on. Plan 02+ will
// extend this with WAClient, Store, etc.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
}

func NewRouter(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Route("/v1", func(r chi.Router) {
		// public
		r.Method(http.MethodGet, "/health", HealthHandler())

		// protected
		r.Group(func(r chi.Router) {
			r.Use(RequireBearerToken(d.Config.Auth.Token))
			r.Method(http.MethodGet, "/status", StatusHandler())
		})
	})

	return r
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/transport/http/...`
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/router.go internal/transport/http/router_test.go
git commit -m "http: chi router with public health and auth-gated status"
```

---

## Task 12: HTTP server lifecycle (bootstrap + graceful shutdown)

**Files:**
- Create: `internal/transport/http/server.go`
- Test: `internal/transport/http/server_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/transport/http/server_test.go`:
```go
package http_test

import (
	"context"
	"io"
	httpcl "net/http"
	"testing"
	"time"

	"github.com/askar/whatsmeow-api/internal/config"
	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerServesHealthAndShutsDown(t *testing.T) {
	srv := httpapi.NewServer(httpapi.Deps{
		Config: config.Config{Server: config.ServerConfig{Bind: "127.0.0.1", Port: 0}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	// wait for the server to bind
	deadline := time.Now().Add(2 * time.Second)
	var addr string
	for {
		addr = srv.Addr()
		if addr != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never reported address")
		}
		time.Sleep(10 * time.Millisecond)
	}

	res, err := httpcl.Get("http://" + addr + "/v1/health")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, httpcl.StatusOK, res.StatusCode)
	_, _ = io.Copy(io.Discard, res.Body)

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/http/... -run TestServerServesHealthAndShutsDown`
Expected: FAIL, `NewServer undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/transport/http/server.go`:
```go
package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type Server struct {
	deps Deps
	addr atomic.Pointer[string]
}

func NewServer(d Deps) *Server { return &Server{deps: d} }

// Addr returns the actual listen address (useful when port=0). Empty until Run binds.
func (s *Server) Addr() string {
	if a := s.addr.Load(); a != nil {
		return *a
	}
	return ""
}

// Run starts the HTTP server. It returns when ctx is cancelled, after a
// graceful 10s shutdown.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.deps.Config.Server.Bind, s.deps.Config.Server.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	resolved := ln.Addr().String()
	s.addr.Store(&resolved)

	server := &http.Server{
		Handler:           NewRouter(s.deps),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/transport/http/... -run TestServerServesHealthAndShutsDown`
Expected: `PASS` (may take ~50ms).

- [ ] **Step 5: Commit**

```bash
git add internal/transport/http/server.go internal/transport/http/server_test.go
git commit -m "http: server with graceful shutdown and dynamic Addr()"
```

---

## Task 13: Cobra root command + serve subcommand

**Files:**
- Modify: `cmd/whatsmeow-api/main.go` (replace stub)
- Create: `cmd/whatsmeow-api/serve.go`

Both files in one task because `main.go` references `serveCmd` from `serve.go`; splitting them would leave the tree broken between commits.

- [ ] **Step 1: Create the serve subcommand file**

Create `cmd/whatsmeow-api/serve.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/askar/whatsmeow-api/internal/config"
	"github.com/askar/whatsmeow-api/internal/logging"
	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
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

			srv := httpapi.NewServer(httpapi.Deps{Config: cfg, Logger: logger})

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

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

- [ ] **Step 2: Replace the main.go stub**

Overwrite `cmd/whatsmeow-api/main.go` with:
```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

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

- [ ] **Step 3: Build and vet**

Run:
```bash
go build ./...
go vet ./...
```
Expected: no output.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add cmd/whatsmeow-api/main.go cmd/whatsmeow-api/serve.go
git commit -m "cmd: cobra root + serve subcommand"
```

---

## Task 14: End-to-end smoke test of the binary

**Files:** none modified (manual check).

- [ ] **Step 1: Build the binary**

Run:
```bash
make build
```
Expected: `bin/whatsmeow-api` exists.

- [ ] **Step 2: Run with default config**

In one terminal:
```bash
./bin/whatsmeow-api serve
```
Expected log line: `... msg="server starting" bind=127.0.0.1 port=8080`.

- [ ] **Step 3: Hit /v1/health**

In another terminal:
```bash
curl -s http://127.0.0.1:8080/v1/health
```
Expected: `{"db":null,"ok":true,"wa_connected":null}`.

- [ ] **Step 4: Hit /v1/status (auth disabled by default)**

```bash
curl -s http://127.0.0.1:8080/v1/status
```
Expected: `{"jid":null,"since":null,"wa_connected":false}`.

- [ ] **Step 5: Verify the unsafe-bind fail-safe**

Stop the daemon. Run:
```bash
WMAPI_SERVER__BIND=0.0.0.0 ./bin/whatsmeow-api serve
```
Expected: exit 1 with `error: validate config: auth.token is required when server.bind is not 127.0.0.1`.

- [ ] **Step 6: Verify auth required when token set**

```bash
WMAPI_AUTH__TOKEN=s3cret ./bin/whatsmeow-api serve
```
In another terminal:
```bash
curl -i http://127.0.0.1:8080/v1/status
```
Expected: `HTTP/1.1 401 Unauthorized` with `application/problem+json`.

```bash
curl -i -H "Authorization: Bearer s3cret" http://127.0.0.1:8080/v1/status
```
Expected: `HTTP/1.1 200 OK`.

- [ ] **Step 7: Stop and document outcome**

Press Ctrl-C. Confirm the log shows `server stopped`. No commit needed for this task — code is unchanged.

---

## Task 15: Update README with quick-start

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace the README body**

Overwrite `README.md` with:
```markdown
# whatsmeow-api

A long-running HTTP/SSE daemon that wraps [`whatsmeow`](https://github.com/tulir/whatsmeow) and exposes a stable JSON API for a single WhatsApp account. Designed primarily as a backend for an MCP server, but usable from any HTTP client.

## Status

Plan 01 (Foundations) shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and a placeholder `/v1/status`. WhatsApp integration lands in Plan 02.

## Quick start

```bash
make build
./bin/whatsmeow-api serve
# in another terminal:
curl http://127.0.0.1:8080/v1/health
```

## Configuration

Source order (highest precedence first):

1. `WMAPI_*` environment variables (use `WMAPI_SECTION__KEY` for nested keys, e.g. `WMAPI_SERVER__PORT=9000`)
2. `--config /path/to/config.toml`
3. Built-in defaults

See `config.example.toml` for the full key list.

### Auth fail-safe

If `auth.token` is empty the daemon refuses to start unless it binds to `127.0.0.1` (or `::1`). Set `auth.token` whenever you bind elsewhere.

## Layout

See `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md` for the full design.

## License

TBD
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README quick-start for Plan 01"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` all PASS
- [ ] Manual smoke from Task 15 all green
- [ ] `git log --oneline` shows ~13 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 02 — waclient + login**.

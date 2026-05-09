# whatsmeow-api Plan 11 — Docker + CI + Examples Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a Docker image to GHCR, run lint+tests on every PR, and ship two examples (docker-compose self-host + curl cookbook). Final v1 milestone.

**Architecture:** Multi-stage Dockerfile builds a static Go binary then copies it into `gcr.io/distroless/static-debian12:nonroot`. Two GitHub Actions workflows: `ci.yml` (lint + test on PR/push) and `release.yml` (GHCR push on push to main and on `v*` tags). Examples live under `examples/`. README gets a CI badge, a "Run with Docker" section, and a Plan 11 status entry.

**Tech Stack:**
- Go 1.26.2 (already in `go.mod`)
- Distroless static-debian12 base image
- golangci-lint v1.64+ (latest stable at impl time)
- GitHub Actions: `actions/checkout@v4`, `actions/setup-go@v5`, `golangci/golangci-lint-action@v6`, `docker/login-action@v3`, `docker/metadata-action@v5`, `docker/build-push-action@v5`

---

## File Structure

| Path | Action | Notes |
|---|---|---|
| `Dockerfile` | NEW | Multi-stage build; distroless runtime |
| `.dockerignore` | NEW | Exclude .git, .worktrees, data, docs/superpowers, etc. |
| `.golangci.yml` | NEW | Conservative linter set |
| `.github/workflows/ci.yml` | NEW | Lint + test jobs |
| `.github/workflows/release.yml` | NEW | GHCR push on main + tags |
| `examples/docker-compose/docker-compose.yml` | NEW | Two profiles (sqlite + postgres) |
| `examples/docker-compose/.env.example` | NEW | Required env vars |
| `examples/docker-compose/README.md` | NEW | Self-host instructions |
| `examples/cookbook.md` | NEW | Curl recipe per endpoint (~27 endpoints) |
| `README.md` | MODIFY | CI badge, Run with Docker section, Plan 11 status entry |
| `internal/store/store.go` | MODIFY | gofmt cleanup (pre-existing drift) |
| `internal/store/sqlite/store_test.go` | MODIFY | gofmt cleanup |
| `internal/config/config_test.go` | MODIFY | gofmt cleanup |

No Go code logic changes. No store / service / HTTP changes.

---

## Task 1: Dockerfile + .dockerignore + local smoke

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

**Goal:** Produce a working ~42 MB image that boots SQLite by default and Postgres when env vars steer it.

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/whatsmeow-api \
    ./cmd/whatsmeow-api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/whatsmeow-api /whatsmeow-api

USER nonroot
EXPOSE 8080
ENTRYPOINT ["/whatsmeow-api"]
CMD ["serve"]
```

> Note: the `-X main.version=` ldflag is harmless if the binary doesn't read a `main.version` variable. If it does (Plan 01 may have wired one), the docker tag flows in. If not, the flag is a no-op and the variable stays at its source-default `"dev"`.

- [ ] **Step 2: Write `.dockerignore`**

```
.git
.github
.worktrees
data
bin
tmp
docs/superpowers/plans
docs/superpowers/specs
*.db
*.db-wal
*.db-shm
testdata
*.log
/coverage*
.env
.env.*
```

`*_test.go` is intentionally NOT excluded — Go's build tooling already ignores them when invoked without `-test`, and excluding them causes the `go mod download`+`go build` cache layer to invalidate every test file change, defeating layer caching. Leave them in the build context.

- [ ] **Step 3: Build and inspect**

```bash
cd /home/askar/src/whatsmeow-api/.worktrees/plan-11-docker
docker build -t whatsmeow-api:dev .
docker images whatsmeow-api:dev
```

Expected: build succeeds in ~60s on a cold cache (download + build); image size ~40-45 MB.

- [ ] **Step 4: Smoke against SQLite (default)**

```bash
docker run --rm -d --name wmapi-test -p 8080:8080 \
    -v /tmp/wmapi-test-data:/data \
    -e WMAPI_DATA_DIR=/data \
    -e WMAPI_AUTH__TOKEN=test-token \
    whatsmeow-api:dev
sleep 3
curl -sS -H "Authorization: Bearer test-token" http://127.0.0.1:8080/v1/status
echo
docker logs wmapi-test 2>&1 | tail -5
docker stop wmapi-test
rm -rf /tmp/wmapi-test-data
```

Expected:
- `/v1/status` returns `{"jid":null,...,"wa_connected":false}`.
- Logs show `app store opened backend=sqlite` and `server starting`.

> Note: the daemon runs as the distroless `nonroot` user (UID 65532). The bind-mounted directory `/tmp/wmapi-test-data` must be writable by that UID. If you see a permission error, `chmod 777 /tmp/wmapi-test-data` once or use a named volume instead. Document this in the docker-compose README (Task 5).

- [ ] **Step 5: Smoke against Postgres**

```bash
docker network create wmapi-test-net 2>/dev/null || true
docker run --rm -d --name pg-test --network wmapi-test-net \
    -e POSTGRES_PASSWORD=test \
    postgres:16-alpine
sleep 4

docker run --rm -d --name wmapi-test --network wmapi-test-net -p 8080:8080 \
    -e WMAPI_AUTH__TOKEN=test-token \
    -e WMAPI_STORAGE__BACKEND=postgres \
    -e WMAPI_STORAGE__POSTGRES_DSN="postgres://postgres:test@pg-test:5432/postgres?sslmode=disable" \
    whatsmeow-api:dev
sleep 3

curl -sS -H "Authorization: Bearer test-token" http://127.0.0.1:8080/v1/status
echo
docker logs wmapi-test 2>&1 | tail -5

docker stop wmapi-test pg-test
docker network rm wmapi-test-net
```

Expected:
- `/v1/status` returns the standard JSON.
- Logs show `app store opened backend=postgres` (no migration errors).

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "docker: multi-stage Dockerfile + .dockerignore (distroless static runtime)"
```

---

## Task 2: golangci-lint config + gofmt sweep

**Files:**
- Create: `.golangci.yml`
- Modify: `internal/store/store.go`
- Modify: `internal/store/sqlite/store_test.go`
- Modify: `internal/config/config_test.go`

**Goal:** A working linter config that runs locally with zero issues. Pre-existing gofmt drift in three files is cleared in the same task so CI doesn't fail on its first run.

- [ ] **Step 1: Write `.golangci.yml`**

```yaml
run:
  timeout: 5m
  tests: true

linters:
  disable-all: true
  enable:
    - gofmt
    - govet
    - staticcheck
    - errcheck
    - unused
    - ineffassign
    - gosimple

issues:
  exclude-dirs:
    - .worktrees
    - data
    - bin

  # Specific exclusions can be added here if a rule fires false positives on
  # legitimate existing code. Empty for now; the implementation plan reviews
  # any noise once the linter runs and fixes call sites in preference to
  # disabling rules.
```

- [ ] **Step 2: Install golangci-lint locally if missing**

```bash
which golangci-lint || \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
golangci-lint --version
```

The exact pinned version is set later in Task 3's CI workflow; locally we use whatever is installed.

- [ ] **Step 3: Run lint and capture issues**

```bash
golangci-lint run ./... 2>&1 | tee /tmp/lint.txt
```

Expected: gofmt complaints on three files (`internal/store/store.go`, `internal/store/sqlite/store_test.go`, `internal/config/config_test.go`). Possibly other issues from `staticcheck` / `errcheck` / `unused`.

- [ ] **Step 4: Apply gofmt sweep**

```bash
gofmt -l .
gofmt -w internal/store/store.go internal/store/sqlite/store_test.go internal/config/config_test.go
gofmt -l .
```

The first `gofmt -l` should list at least the three pre-existing dirty files; after the `-w` step, the second `gofmt -l` should print nothing for those three.

- [ ] **Step 5: Triage other linter findings**

For each non-gofmt finding from Step 3:
- `errcheck` on a test helper or a defer cleanup: explicitly assign to `_ = ...` to acknowledge the discard.
- `staticcheck` style suggestions (`SA*`, `S*`): apply the suggested fix.
- `unused` on dead code: delete the dead code.
- `ineffassign` on unused assignment: delete or fix the bug.
- `gosimple` (`S*`): apply the suggested fix.

If a linter fires a true false positive on legitimately-clear code, prefer a `//nolint:<linter>` directive on the specific line over disabling the rule globally. Such cases should be rare with this conservative ruleset.

- [ ] **Step 6: Verify clean lint**

```bash
golangci-lint run ./...
```

Expected: zero output (success).

- [ ] **Step 7: Verify tests still pass**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: full repo green.

- [ ] **Step 8: Commit**

```bash
git add .golangci.yml internal/store/store.go internal/store/sqlite/store_test.go internal/config/config_test.go
# add any other files touched by Step 5's triage
git commit -m "lint: golangci.yml baseline + gofmt sweep on 3 pre-existing dirty files"
```

---

## Task 3: CI workflow (lint + test)

**Files:**
- Create: `.github/workflows/ci.yml`

**Goal:** Every PR and push to main runs lint + test (both dialects via testcontainers) in under ~5 min.

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - uses: golangci/golangci-lint-action@v6
        with:
          version: v1.64

  test:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - run: go mod download

      - run: go test -race -count=1 ./...
```

- [ ] **Step 2: Validate the YAML locally**

```bash
yamllint .github/workflows/ci.yml || echo "yamllint not installed; skipping"
```

If `yamllint` isn't installed, validate by eye. The workflow YAML schema is checked by GitHub on push.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: GitHub Actions workflow for lint + test (both dialects via testcontainers)"
```

> The workflow runs on the eventual PR push (Task 7's commit triggers it). For a faster sanity check, the implementer can push the branch early and let the workflow run on the open PR.

---

## Task 4: Release workflow (GHCR push)

**Files:**
- Create: `.github/workflows/release.yml`

**Goal:** Push to GHCR on every push to `main` (with `:main` and `:sha-<short>` tags) and on `v*` tags (with `:<version>` and `:latest`).

- [ ] **Step 1: Write `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    branches: [main]
    tags: ['v*']

permissions:
  contents: read
  packages: write

jobs:
  publish:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=ref,event=branch
            type=sha,prefix=sha-
            type=semver,pattern={{version}}
            type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}

      - uses: docker/build-push-action@v5
        with:
          context: .
          platforms: linux/amd64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: GHCR release workflow (amd64 image on push to main + v* tags)"
```

> The workflow needs `packages: write` on `GITHUB_TOKEN`. The repo's "Settings → Actions → General → Workflow permissions" must be set to "Read and write permissions" or this fails with HTTP 403 on first push. The PR description (Task 7) calls this out so the user can flip the toggle once.

---

## Task 5: docker-compose example

**Files:**
- Create: `examples/docker-compose/docker-compose.yml`
- Create: `examples/docker-compose/.env.example`
- Create: `examples/docker-compose/README.md`

**Goal:** A new operator can pair their account in three commands.

- [ ] **Step 1: Write `examples/docker-compose/docker-compose.yml`**

```yaml
name: whatsmeow-api

services:
  daemon:
    image: ghcr.io/askarzh/whatsmeow-api:main
    profiles: ["sqlite", "postgres"]
    ports:
      - "8080:8080"
    environment:
      WMAPI_AUTH__TOKEN: ${WMAPI_AUTH_TOKEN}
      WMAPI_STORAGE__BACKEND: ${WMAPI_STORAGE_BACKEND:-sqlite}
      WMAPI_STORAGE__POSTGRES_DSN: ${WMAPI_STORAGE_POSTGRES_DSN:-}
      WMAPI_DATA_DIR: /data
      WMAPI_SERVER__BIND: "0.0.0.0"
    volumes:
      - daemon-data:/data
    depends_on:
      postgres:
        condition: service_healthy
        required: false

  postgres:
    image: postgres:16-alpine
    profiles: ["postgres"]
    environment:
      POSTGRES_USER: whatsmeow
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-whatsmeow}
      POSTGRES_DB: whatsmeow_api
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U whatsmeow"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  daemon-data:
  postgres-data:
```

> The `WMAPI_SERVER__BIND=0.0.0.0` env var is critical: by default the daemon binds to `127.0.0.1` (Plan 01 fail-safe), which from inside a container means "localhost of the container", so the host port mapping won't see traffic. Setting `0.0.0.0` here is paired with the required `WMAPI_AUTH__TOKEN` (the Plan 01 fail-safe enforces that one of the two must be set).

- [ ] **Step 2: Write `examples/docker-compose/.env.example`**

```
# REQUIRED: bearer token for HTTP API auth.
# The daemon binds to 0.0.0.0 inside the container (so host port mapping
# works), so the Plan 01 fail-safe REQUIRES this to be a non-empty value.
# Use a strong random string (e.g. `openssl rand -hex 32`).
WMAPI_AUTH_TOKEN=change-me-to-a-strong-secret

# Choose the storage backend.
# "sqlite" -- simpler, single-container, file in the volume.
# "postgres" -- requires `docker compose --profile postgres up`.
WMAPI_STORAGE_BACKEND=sqlite

# REQUIRED when WMAPI_STORAGE_BACKEND=postgres.
# The hostname `postgres` matches the service name in docker-compose.yml.
# WMAPI_STORAGE_POSTGRES_DSN=postgres://whatsmeow:whatsmeow@postgres:5432/whatsmeow_api?sslmode=disable

# Optional: Postgres password (used by the postgres service).
# POSTGRES_PASSWORD=whatsmeow
```

- [ ] **Step 3: Write `examples/docker-compose/README.md`**

```markdown
# Self-host whatsmeow-api with docker-compose

Two profiles ship in this directory:

- **sqlite** — single container, file-backed. Simpler.
- **postgres** — daemon + Postgres container. Production-shaped.

## Quickstart (SQLite)

    cp .env.example .env
    # edit .env: set WMAPI_AUTH_TOKEN to a strong random value
    #            (e.g. `openssl rand -hex 32`)

    docker compose --profile sqlite up -d

The daemon listens on `http://localhost:8080`. The data directory is a
named Docker volume (`whatsmeow-api_daemon-data`), so your pairing
survives restarts.

## Quickstart (Postgres)

    cp .env.example .env
    # edit .env:
    #   WMAPI_AUTH_TOKEN=...
    #   WMAPI_STORAGE_BACKEND=postgres
    #   WMAPI_STORAGE_POSTGRES_DSN=postgres://whatsmeow:whatsmeow@postgres:5432/whatsmeow_api?sslmode=disable
    #   POSTGRES_PASSWORD=whatsmeow  # any value, must match the DSN

    docker compose --profile postgres up -d

## Pair your WhatsApp account

After the daemon is up, scan a QR code with your phone:

    export WMAPI_TOKEN=<the value you set in .env>

    curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
         http://localhost:8080/v1/login/qr

The SSE stream emits `qr` events containing the data to encode. Render
the QR (any library that accepts a string) and scan it on
WhatsApp → Settings → Linked Devices → Link a Device.

Alternatively, request a phone-pair code:

    curl -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
         -H "Content-Type: application/json" \
         -d '{"phone":"+15551234567"}' \
         http://localhost:8080/v1/login/phone

The 8-character code is emitted on the SSE response stream; type it on
the linked-device screen.

After pairing, `curl -H "Authorization: Bearer $WMAPI_TOKEN"
http://localhost:8080/v1/status` returns your JID and
`wa_connected: true`.

## Inspecting the running container

The image is distroless (no shell), so `docker compose exec daemon sh`
won't work. Use logs and HTTP for diagnostics:

    docker compose logs -f daemon
    curl -H "Authorization: Bearer $WMAPI_TOKEN" \
         http://localhost:8080/v1/stats

## Bind mounts vs. named volumes

The compose file uses a named volume for `/data`. If you prefer a bind
mount (e.g. `- ./data:/data`), make sure the host directory is writable
by UID 65532 (the `nonroot` user inside the distroless image):

    mkdir -p ./data && sudo chown 65532:65532 ./data

## Tearing down

    docker compose --profile sqlite down            # keeps the volume
    docker compose --profile sqlite down --volumes  # deletes pairing data

See `../cookbook.md` for a curl recipe per HTTP endpoint.
```

- [ ] **Step 4: Local smoke**

```bash
cd examples/docker-compose
cp .env.example .env
# Edit .env: set WMAPI_AUTH_TOKEN=local-test-token
sed -i 's/change-me-to-a-strong-secret/local-test-token/' .env

# Need a built image first; if Task 1's `whatsmeow-api:dev` is still around,
# tweak the compose file temporarily to use it, OR build locally and tag.
docker tag whatsmeow-api:dev ghcr.io/askarzh/whatsmeow-api:main 2>/dev/null

docker compose --profile sqlite up -d
sleep 3
curl -sS -H "Authorization: Bearer local-test-token" http://localhost:8080/v1/status
echo
docker compose --profile sqlite down --volumes
```

Expected: `/v1/status` returns the standard JSON.

- [ ] **Step 5: Smoke the postgres profile**

```bash
sed -i 's/^WMAPI_STORAGE_BACKEND=.*/WMAPI_STORAGE_BACKEND=postgres/' .env
echo 'WMAPI_STORAGE_POSTGRES_DSN=postgres://whatsmeow:whatsmeow@postgres:5432/whatsmeow_api?sslmode=disable' >> .env
echo 'POSTGRES_PASSWORD=whatsmeow' >> .env

docker compose --profile postgres up -d
sleep 6
curl -sS -H "Authorization: Bearer local-test-token" http://localhost:8080/v1/status
echo
docker compose logs daemon | tail -5

docker compose --profile postgres down --volumes
rm .env
```

Expected: `/v1/status` returns the standard JSON; logs show `app store opened backend=postgres`.

- [ ] **Step 6: Commit**

```bash
git add examples/docker-compose/
git commit -m "examples: docker-compose with sqlite + postgres profiles"
```

---

## Task 6: Curl cookbook

**Files:**
- Create: `examples/cookbook.md`

**Goal:** A copy-pasteable curl recipe for every HTTP endpoint.

The list of endpoints is `internal/transport/http/router.go` — re-read it before writing the cookbook to ensure no endpoint is missed. As of Plan 10 there are 27 routes (plus `/v1/health` outside the auth group).

- [ ] **Step 1: Inventory endpoints**

```bash
grep -E "r\.Method\(http\." internal/transport/http/router.go
```

Use the output as the source-of-truth list when writing each cookbook section.

- [ ] **Step 2: Write `examples/cookbook.md`**

```markdown
# whatsmeow-api Cookbook

Copy-pasteable curl recipes for every HTTP endpoint. Assumes the daemon
runs at `http://localhost:8080` with a bearer token in
`$WMAPI_TOKEN`.

```bash
export WMAPI_BASE=http://localhost:8080
export WMAPI_TOKEN=your-bearer-token
```

All examples assume `jq` is installed for pretty-printing JSON.

## Health and status

### Liveness probe (no auth)

```bash
curl -sS "$WMAPI_BASE/v1/health"
# → {"status":"ok"}
```

### Daemon status

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" "$WMAPI_BASE/v1/status" | jq
# → {"jid":"15551234567@s.whatsapp.net","push_name":"...","since":"...","wa_connected":true}
```

## Login

### QR code (recommended)

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/login/qr"
# Streams SSE: `event: qr` with the encoded payload to display, then
# `event: paired` on success.
```

### Phone-pair code

```bash
curl -N -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"phone":"+15551234567"}' \
     "$WMAPI_BASE/v1/login/phone"
# Streams SSE: `event: code` with the 8-character code, then `event: paired`.
```

### Logout

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/logout"
# → 204 No Content
```

## Messages

### Send a text

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"chat_jid":"15557654321@s.whatsapp.net","body":"hello"}' \
     "$WMAPI_BASE/v1/messages" | jq
# → {"id":"3EB0...","chat_jid":"...","body":"hello",...}
```

### Reply to a message

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"chat_jid":"15557654321@s.whatsapp.net","body":"reply","reply_to":"3EB0..."}' \
     "$WMAPI_BASE/v1/messages" | jq
```

### Edit your message

```bash
curl -sS -X PATCH -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"body":"edited body"}' \
     "$WMAPI_BASE/v1/messages/3EB0..." | jq
```

### Delete (revoke) your message

```bash
curl -sS -X DELETE -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0..."
# → 204 No Content
```

## Media

### Send media (image, document, audio, video, sticker)

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -F "chat_jid=15557654321@s.whatsapp.net" \
     -F "kind=image" \
     -F "caption=look at this" \
     -F "file=@/path/to/photo.jpg" \
     "$WMAPI_BASE/v1/media" | jq
```

### Download persisted media bytes

```bash
curl -sS -OJ -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/media/3EB0..."
# Saves the file with the original filename via Content-Disposition.
```

## Reactions

### Add a reaction

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"emoji":"👍"}' \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions"
# → 204
```

### Clear your reaction

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"emoji":""}' \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions"
```

### List reactions on a message

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions" | jq
```

## Receipts and typing

### Mark a received message as read

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../read"
# → 204
```

### Send "composing" / "paused" presence

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"state":"composing"}' \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net/typing"
# Pair with `{"state":"paused"}` when the user stops typing.
```

### List delivery / read receipts for a message

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../receipts" | jq
```

## Chats and search

### List chats (cursor pagination)

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats?limit=50" | jq
```

### Get one chat

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net" | jq
```

### List messages in a chat

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net/messages?limit=50" | jq
```

### Search messages (full-text)

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/search?q=hello&limit=20" | jq
```

### List contacts

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/contacts" | jq
```

### Search contacts

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/contacts/search?q=alice" | jq
```

### Stats

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/stats" | jq
```

## Groups

### Create a group

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"name":"Project X","participants":["15557654321@s.whatsapp.net"]}' \
     "$WMAPI_BASE/v1/groups" | jq
```

### List members

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/members" | jq
```

### Add or remove members

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"action":"add","participants":["15559876543@s.whatsapp.net"]}' \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/members" | jq

# Remove with action:"remove"
```

### Leave a group

```bash
curl -sS -X DELETE -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/membership"
# → 204
```

## SSE event stream

### Subscribe (live tail from now)

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     "$WMAPI_BASE/v1/events"
```

You'll see a `:ready` comment, then a synthetic `connection.state` frame
at `id: 0`, then real events as they happen.

### Resume from a known sequence

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     -H "Last-Event-ID: 4271" \
     "$WMAPI_BASE/v1/events"
# Replays everything with seq > 4271, then live-tails.
```

Or via query param:

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     "$WMAPI_BASE/v1/events?since=4271"
```

### Event types (per Plan 09)

- `message.received` — inbound text/media
- `message.edited` — inbound EDIT
- `message.deleted` — inbound REVOKE
- `reaction.received` — inbound reaction (set or clear)
- `receipt.received` — delivered/read/played receipt
- `connection.state` — login/disconnect/reconnect transitions

Each event payload carries `"v": 1` for forward compatibility.
``` 

> Important: when writing the cookbook above, verify each curl example against the actual handler. Some endpoint shapes may differ from this template (request body field names, path params); cross-check the corresponding `internal/transport/http/<file>.go` and adjust. The plan can't promise the exact JSON shape of every request body without re-reading every handler — the implementer does that pass.

- [ ] **Step 3: Cross-check against the router**

For each endpoint in `internal/transport/http/router.go`, confirm the cookbook has a section. Use this checklist:
- `GET /v1/health` ✓
- `GET /v1/status`
- `POST /v1/login/qr`, `/v1/login/phone`
- `POST /v1/logout`
- `POST /v1/messages`, `PATCH /v1/messages/{id}`, `DELETE /v1/messages/{id}`
- `GET /v1/chats`, `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages`
- `GET /v1/messages/search`, `GET /v1/contacts`, `GET /v1/contacts/search`
- `GET /v1/stats`
- `POST /v1/media`, `GET /v1/media/{message_id}`
- `POST /v1/messages/{id}/reactions`, `GET /v1/messages/{id}/reactions`
- `POST /v1/messages/{id}/read`, `GET /v1/messages/{id}/receipts`
- `POST /v1/chats/{jid}/typing`
- `POST /v1/groups`, `GET /v1/groups/{jid}/members`, `POST /v1/groups/{jid}/members`, `DELETE /v1/groups/{jid}/membership`
- `GET /v1/events`

If any handler request body shape (struct field names) differs from the cookbook's example, adjust the cookbook to match the source. The handlers are the source of truth.

- [ ] **Step 4: Commit**

```bash
git add examples/cookbook.md
git commit -m "examples: cookbook.md (curl recipe per HTTP endpoint)"
```

---

## Task 7: README polish + final smoke

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add CI badge under the title**

Edit `README.md`. After the first heading and tagline, insert:

```markdown
[![CI](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml/badge.svg)](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml)
```

- [ ] **Step 2: Add a "Run with Docker" section**

After the existing "Quick start" section, insert:

```markdown
## Run with Docker

Pull the latest dev image and run against SQLite:

```bash
docker run --rm -p 8080:8080 \
           -v $(pwd)/data:/data \
           -e WMAPI_AUTH__TOKEN=change-me \
           -e WMAPI_SERVER__BIND=0.0.0.0 \
           ghcr.io/askarzh/whatsmeow-api:main
```

For a self-host setup with Postgres, see
[`examples/docker-compose/`](examples/docker-compose/).

For a curl recipe per endpoint, see
[`examples/cookbook.md`](examples/cookbook.md).
```

- [ ] **Step 3: Add Plan 11 status entry**

Append after the Plan 10 status line:

```markdown
- **Plan 11 (Docker + CI + examples)** shipped: image published to
  `ghcr.io/askarzh/whatsmeow-api` on every push to main and on `v*` tags;
  GitHub Actions CI runs `golangci-lint` + `go test -race` (both dialects
  via testcontainers) on every PR; `examples/docker-compose/` provides a
  self-host setup with sqlite + postgres profiles;
  `examples/cookbook.md` documents every HTTP endpoint with
  copy-pasteable curl recipes.
```

Replace the trailing roadmap line:

```markdown
v1 is complete. Future work — outbound message lifecycle events,
group-lifecycle deltas, video/audio/sticker outbound, multi-arch image,
helm chart — is tracked in the spec backlog and lives in follow-up plans
as real consumer needs surface.
```

- [ ] **Step 4: Final repo build + lint + test**

```bash
go build ./...
golangci-lint run ./...
go test ./... -race
```

Expected: all green.

- [ ] **Step 5: Final image build + smoke**

```bash
docker build -t whatsmeow-api:dev .
docker run --rm -d --name wmapi-final-test -p 8080:8080 \
    -v /tmp/wmapi-final:/data \
    -e WMAPI_AUTH__TOKEN=test \
    -e WMAPI_SERVER__BIND=0.0.0.0 \
    whatsmeow-api:dev
sleep 3
curl -sS -H "Authorization: Bearer test" http://127.0.0.1:8080/v1/status
echo
docker stop wmapi-final-test
rm -rf /tmp/wmapi-final
```

Expected: `/v1/status` returns the standard JSON. The README's run-with-docker snippet works as documented.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: README CI badge + Run with Docker section + Plan 11 status entry"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `golangci-lint run ./...` clean (zero output)
- [ ] `go test ./... -race` PASS (both dialects, all packages)
- [ ] `docker build -t whatsmeow-api:dev .` succeeds; image ~42 MB
- [ ] `docker run` SQLite smoke: `/v1/status` returns valid JSON
- [ ] `docker run` Postgres smoke: `/v1/status` returns valid JSON, no migration errors
- [ ] `docker compose --profile sqlite up -d` smoke
- [ ] `docker compose --profile postgres up -d` smoke
- [ ] `examples/cookbook.md` covers all 27 endpoints in `router.go`
- [ ] `git log --oneline` shows ~7 well-scoped commits

When all the above are checked, this plan is complete and **v1 is shipped**.

After merge, the user must verify GitHub repo settings:
- **Settings → Actions → General → Workflow permissions** is "Read and write permissions" (so `release.yml` can push to GHCR).

The first push to main triggers the Release workflow; the resulting image is visible at https://github.com/askarzh/whatsmeow-api/pkgs/container/whatsmeow-api.

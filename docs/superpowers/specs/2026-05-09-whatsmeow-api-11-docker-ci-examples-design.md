# whatsmeow-api Plan 11 — Docker Image, CI, Examples, Docs Polish

**Date:** 2026-05-09
**Status:** Design — ready for implementation plan
**Predecessors:** Plans 01–10 shipped on `main`. This is the final v1 milestone (per the master design doc §14).

## 1. Goals

Make the daemon deployable and onboard-able by a real user without source-building. Specifically:

- A Docker image is published to GHCR on every push to `main` and on every release tag.
- A GitHub Actions workflow runs lint + tests (both dialects) on every PR.
- A `docker-compose` example lets a new operator pair their WhatsApp account in three commands.
- A curl cookbook documents every HTTP endpoint with copy-pasteable examples.
- The README has a "Run with Docker" section and a CI status badge.

After this plan, v1 is complete.

## 2. Non-goals

- **Multi-arch image (linux/arm64).** amd64 only for v1; arm64 builds in a follow-up if a real consumer needs it.
- **cosign image signing, SBOM, provenance attestations.** Worthwhile for hardened consumers but YAGNI until someone asks.
- **A separate documentation site (mkdocs / Hugo / etc.).** README + cookbook is enough; static-site generation can land if the content grows past one file.
- **Helm chart, Terraform module, systemd unit.** Out of scope; docker-compose covers the immediate self-host case.
- **Release-notes automation, semver enforcement, conventional-commits gate.** A future plan can add release tooling once v1 ships.
- **CI matrix across Go versions or OSes.** Single matrix entry: Go 1.26 on `ubuntu-latest`. We can expand if a contributor needs it.
- **Cookbook executable verification (httpyac, integration tests against a paired daemon).** Acceptable drift risk for v1.

## 3. Architecture

The daemon today is a single Go binary that links statically (modernc.org/sqlite is pure-Go; no CGO). The container image is therefore minimal:

```
┌─────────────────────────────────────┐
│  golang:1.26-alpine    (builder)    │
│  CGO_ENABLED=0                      │
│  go build -ldflags="-s -w"          │
│  → /out/whatsmeow-api               │
└──────────────────┬──────────────────┘
                   │ COPY
                   ▼
┌─────────────────────────────────────┐
│  gcr.io/distroless/static-debian12  │
│  USER nonroot                       │
│  EXPOSE 8080                        │
│  ENTRYPOINT ["/whatsmeow-api"]      │
│  CMD ["serve"]                      │
└─────────────────────────────────────┘
```

Image is configured purely via environment variables (already supported by the koanf-based config — `WMAPI_*` names map to TOML paths). The image carries no default config file; operators either bind-mount one at `/config.toml` or set env vars.

CI runs in two workflows:
- `ci.yml` — runs on PRs and pushes; gates merge.
- `release.yml` — runs on push to `main` and on `v*` tags; pushes to GHCR.

## 4. Dockerfile

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/whatsmeow-api \
    ./cmd/whatsmeow-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/whatsmeow-api /whatsmeow-api
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/whatsmeow-api"]
CMD ["serve"]
```

Choices:
- **Distroless `static-debian12:nonroot`** because the binary is fully static — no glibc/musl runtime needed. The base image is ~2 MB; the final image is dominated by the Go binary (~40 MB stripped). Total ~42 MB.
- **`USER nonroot`** so the container does not run as root by default.
- **`-ldflags="-s -w"`** strips the debug info; saves ~25% binary size. Stack traces still work; we don't need symbol tables in production.
- **`./cmd/whatsmeow-api`** is the existing module entry point.

The `serve` subcommand is the daemon entry. Other subcommands (`login qr`, `status`, etc.) are CLI clients that talk to a running daemon and have less reason to run inside the container — but `docker exec` (or `docker run --rm ... whatsmeow-api status`) still works because the binary is the same.

A `.dockerignore` keeps the build context lean:

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
*_test.go
testdata
```

> Note: excluding `*_test.go` is intentional — the runtime image doesn't need tests, and excluding them shrinks the build context. The Go build itself doesn't compile `*_test.go` files when invoked without `-test`.

## 5. CI workflow (`.github/workflows/ci.yml`)

Triggered on:
- `pull_request` to any branch.
- `push` to `main`.

Two jobs, run in parallel:

**Job: lint**
- `actions/checkout@v4`
- `actions/setup-go@v5` with `go-version: '1.26'` and `cache: true`.
- `golangci/golangci-lint-action@v6` with version pinned to a recent stable release.

**Job: test**
- `actions/checkout@v4`
- `actions/setup-go@v5` with `go-version: '1.26'` and `cache: true`.
- `go mod download`
- `go test -race -count=1 ./...`

Postgres tests use testcontainers-go, which works out-of-the-box on `ubuntu-latest` runners (Docker is preinstalled). No `services:` block needed.

Total expected wall time: ~5 min (test job dominated by ~100s of Postgres testcontainers + Go build).

## 6. `.golangci.yml`

Conservative baseline:

```yaml
run:
  timeout: 5m

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
```

Pre-existing gofmt drift in three files surfaces immediately when this lands:
- `internal/store/store.go`
- `internal/store/sqlite/store_test.go`
- `internal/config/config_test.go`

The implementation plan runs `gofmt -w` on those three files as part of Task 2 to clear the gate.

If `staticcheck` or `errcheck` fires on legitimate existing code, the plan fixes the call site rather than disabling the rule. If a rule is too noisy for our codebase (unlikely with this conservative set), it can be disabled in `.golangci.yml`.

## 7. Release workflow (`.github/workflows/release.yml`)

Triggered on:
- `push` to `main` → publishes `:main` and `:sha-<short>` tags.
- `push` of tags matching `v*` → publishes `:<version>` (e.g. `:1.0.0`) and `:latest`.

Job steps:
1. `actions/checkout@v4`
2. `docker/login-action@v3` against `ghcr.io` using `${{ secrets.GITHUB_TOKEN }}`.
3. `docker/metadata-action@v5` to compute tag list and labels.
4. `docker/build-push-action@v5` with `platforms: linux/amd64`, `push: true`.

Permissions block:
```yaml
permissions:
  contents: read
  packages: write
```

The repository's "Workflow permissions" setting (under Settings → Actions → General) must allow read-and-write for `GITHUB_TOKEN` to push to GHCR. The PR description calls this out so the user verifies the toggle.

Image: `ghcr.io/askarzh/whatsmeow-api` (or whatever `${{ github.repository }}` resolves to — the workflow uses that).

## 8. Examples

### 8.1 `examples/docker-compose/`

Three files:

**`docker-compose.yml`** with two profiles:

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

**`.env.example`**:

```
# Required when bound to a non-loopback interface.
# Plan 01 fail-safe: empty token allowed only when bind=127.0.0.1.
# For docker-compose the daemon binds to 0.0.0.0 inside the container, so
# you MUST set this to a real value.
WMAPI_AUTH_TOKEN=change-me

# Choose backend. "sqlite" is the simpler default; "postgres" requires the
# Postgres profile (`docker compose --profile postgres up -d`).
WMAPI_STORAGE_BACKEND=sqlite

# Required when WMAPI_STORAGE_BACKEND=postgres
# WMAPI_STORAGE_POSTGRES_DSN=postgres://whatsmeow:whatsmeow@postgres:5432/whatsmeow_api?sslmode=disable
# POSTGRES_PASSWORD=whatsmeow
```

**`README.md`** (in `examples/docker-compose/`) explains:

```markdown
# Self-host whatsmeow-api with docker-compose

## SQLite (single container, file-backed)

    cp .env.example .env
    # edit .env: set WMAPI_AUTH_TOKEN to a strong value
    docker compose --profile sqlite up -d

## Postgres (daemon + Postgres container)

    cp .env.example .env
    # edit .env: set WMAPI_AUTH_TOKEN, uncomment and set WMAPI_STORAGE_POSTGRES_DSN
    docker compose --profile postgres up -d

## Pair your WhatsApp account

After the daemon is up:

    export WMAPI_TOKEN=<the same token you set in .env>

    # QR pairing (recommended)
    curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
         http://localhost:8080/v1/login/qr

    # ... or phone-code pairing
    curl -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
         -H "Content-Type: application/json" \
         -d '{"phone":"+15551234567"}' \
         http://localhost:8080/v1/login/phone

See `examples/cookbook.md` for every other endpoint.
```

### 8.2 `examples/cookbook.md`

A single Markdown file. Sections, in order:

1. **Setup** — env vars, base URL, auth header.
2. **Status** (`GET /v1/status`).
3. **Login**:
   - QR (`POST /v1/login/qr`, SSE stream)
   - Phone code (`POST /v1/login/phone`, SSE stream)
4. **Logout** (`POST /v1/logout`).
5. **Messages**:
   - Send text (`POST /v1/messages`)
   - Send text with reply (`POST /v1/messages` with `reply_to`)
   - Edit (`PATCH /v1/messages/{id}`)
   - Delete (`DELETE /v1/messages/{id}`)
6. **Media**:
   - Send (`POST /v1/media` multipart)
   - Get (`GET /v1/media/{message_id}`)
7. **Reactions**:
   - Send (`POST /v1/messages/{id}/reactions`)
   - Clear (same endpoint with empty emoji)
   - List (`GET /v1/messages/{id}/reactions`)
8. **Receipts and typing**:
   - Mark read (`POST /v1/messages/{id}/read`)
   - Send typing (`POST /v1/chats/{jid}/typing`)
   - List receipts (`GET /v1/messages/{id}/receipts`)
9. **Chats and search**:
   - List chats (`GET /v1/chats`)
   - Get chat (`GET /v1/chats/{jid}`)
   - List messages (`GET /v1/chats/{jid}/messages`)
   - Search messages (`GET /v1/messages/search?q=`)
   - List contacts (`GET /v1/contacts`)
   - Search contacts (`GET /v1/contacts/search?q=`)
   - Stats (`GET /v1/stats`)
10. **Groups**:
    - Create (`POST /v1/groups`)
    - List members (`GET /v1/groups/{jid}/members`)
    - Add/remove (`POST /v1/groups/{jid}/members`)
    - Leave (`DELETE /v1/groups/{jid}/membership`)
11. **SSE event stream**:
    - Connect (`curl -N`)
    - Resume with `Last-Event-ID`
    - Resume with `?since=`

Each section: 1-3 sentences of context, then a fenced code block with a copy-pasteable curl. Auth header (`-H "Authorization: Bearer $WMAPI_TOKEN"`) appears in every example.

The cookbook is markdown only — no executable doc-test framework. Drift risk is acknowledged in §10.

## 9. README polish

Specific changes:

- **Add a "Run with Docker" section** near the top, right after the existing "Status" block:

  ```markdown
  ## Run with Docker

  Pull the latest image from GHCR and run against SQLite:

      docker run --rm -p 8080:8080 \
                 -v $(pwd)/data:/data \
                 -e WMAPI_AUTH__TOKEN=change-me \
                 ghcr.io/askarzh/whatsmeow-api:main

  See `examples/docker-compose/` for a self-host setup with Postgres,
  and `examples/cookbook.md` for a curl recipe per endpoint.
  ```

- **Update the trailing roadmap line** to remove the "Docker image and CI workflow land in Plan 11" bullet — that lands here. Replace with: "v1 is complete. Future work on outbound message lifecycle events, group-lifecycle deltas, and video/audio/sticker outbound is tracked in the spec backlog."

- **Add a CI status badge** at the top of the README, just under the title:

  ```markdown
  [![CI](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml/badge.svg)](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml)
  ```

- **Add a "v1 done" entry** to the Status section:

  ```markdown
  - **Plan 11 (Docker + CI + examples)** shipped: published image at
    `ghcr.io/askarzh/whatsmeow-api`; GitHub Actions CI runs lint + tests
    (both dialects) on every PR; `examples/docker-compose/` has a self-host
    setup with `sqlite` and `postgres` profiles; `examples/cookbook.md`
    documents every HTTP endpoint with copy-pasteable curl recipes.
  ```

## 10. Risks and trade-offs

1. **GHCR push permission.** `.github/workflows/release.yml` declares `packages: write`. If the repo's "Workflow permissions" is set to "Read repository contents only" the push fails. The PR description calls this out so the user can flip the toggle once.

2. **Distroless has no shell.** `docker exec -it ... sh` doesn't work, which surprises operators used to Alpine images. Mitigation: docker-compose README documents that `kubectl debug` / sidecar containers / `docker run --rm ...:debug` (a future debug variant) are the escape hatches. Trade-off accepted for the smaller, harder-to-attack image.

3. **CI Postgres tests on testcontainers.** Each test boots its own container (~2.4s). Total ~100s for the Postgres suite under `-race`. Acceptable for v1; if it ever hurts, the `resetTables` helper from Plan 10 can be wired to share a single container per test package, dropping the suite to ~15s.

4. **Cookbook drift.** Once an HTTP shape changes, the cookbook silently drifts. There's no automated check. v1 accepts this; a future plan could add executable docs (httpyac, schemathesis) or a smoke test that runs the cookbook against a paired daemon.

5. **`golangci.yml` baseline triggers existing gofmt drift.** Three files (`internal/store/store.go`, `internal/store/sqlite/store_test.go`, `internal/config/config_test.go`) are already gofmt-dirty pre-Plan-11. The implementation plan cleans them as part of Task 2, before CI lands. If `staticcheck` or `errcheck` fires false positives on existing code, fix the call site (preferred) or disable the rule in `.golangci.yml` (last resort).

6. **`:main` tag on every push.** Some operators pin `:latest`; we deliberately don't push `:latest` from `main`. `:latest` is reserved for tagged releases. Document in README.

7. **Image arch.** `linux/amd64` only. Apple Silicon developers can build locally. Multi-arch defers to a follow-up plan.

8. **Cookbook is markdown-only.** No fancy generation. If the cookbook grows past ~500 lines, the next plan can split it into per-feature pages or build a static site. v1 ships one file.

## 11. Open questions for the implementation plan (not blockers for spec)

- **Exact `golangci-lint` version pin.** Pick whatever is the latest stable at impl time; pin in the workflow YAML.
- **Whether to commit `docker-compose.yml` with `:main` or `:latest`.** Spec proposes `:main` because the user is likely to run before a `v1.0.0` tag exists. The implementation plan can revisit if the user prefers.
- **Whether `.dockerignore` should also exclude the docs/ tree.** Probably yes (smaller build context, no docs in the image), but the plan revisits.
- **Whether to publish a `:dev` or `:edge` tag too.** Spec proposes only `:main` and `:sha-<short>` from main pushes; `:edge` would be redundant.
- **Whether the cookbook should include MCP-server-integration snippets.** Out of scope for v1; the cookbook is HTTP-only.

## 12. Approval gate

Estimated **7 implementation tasks**:

1. **Dockerfile + .dockerignore + local image build + smoke.** `docker build` clean, `docker run` boots SQLite, `docker run -e WMAPI_STORAGE__BACKEND=postgres -e WMAPI_STORAGE__POSTGRES_DSN=...` boots Postgres against a sibling container. `docker images` shows ~42 MB.
2. **`.golangci.yml` + gofmt sweep on the 3 dirty files.** `golangci-lint run` clean locally. Existing tests still pass.
3. **`.github/workflows/ci.yml` (lint + test job).** Push to a draft PR; verify both jobs go green.
4. **`.github/workflows/release.yml` (GHCR push on main + tags).** Push to a draft PR; the workflow will run on the eventual merge to main. Test by tagging a throwaway `v0.0.1-test` once and verifying the image lands.
5. **`examples/docker-compose/` (compose file + .env.example + per-dir README).** Manual smoke: bring up sqlite profile, hit `/v1/status`, tear down. Repeat with postgres profile.
6. **`examples/cookbook.md` (curl recipes for every endpoint).** Cross-reference the router; one snippet per endpoint. No automated check.
7. **README polish + CI badge + v1-done status entry + final smoke.** Build the image one more time, verify the README's run-with-docker snippet works, push, open PR.

Date: 2026-05-09.

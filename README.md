# whatsmeow-api

[![CI](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml/badge.svg)](https://github.com/askarzh/whatsmeow-api/actions/workflows/ci.yml)

A long-running HTTP/SSE daemon that wraps [`whatsmeow`](https://github.com/tulir/whatsmeow) and exposes a stable JSON API for a single WhatsApp account. Designed primarily as a backend for an MCP server, but usable from any HTTP client.

## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.
- **Plan 03 (app store)** shipped: SQLite-backed persistence layer with seven tables (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) and `golang-migrate`-driven schema migrations that auto-run on `serve`.
- **Plan 04 (send + receive)** shipped: `POST /v1/messages` sends a text message via whatsmeow and persists the outbound row. Inbound message events from whatsmeow are persisted automatically (text + media kinds; media metadata lands in Plan 06). `chats.last_msg_at`, `chats.unread_count`, and `contacts.push_name` update in real time.
- **Plan 05 (list + search)** shipped: read-side endpoints over the app store. `GET /v1/chats` (cursor pagination), `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages` (cursor pagination), `GET /v1/messages/search?q=`, `GET /v1/contacts`, `GET /v1/contacts/search?q=`, `GET /v1/stats`.
- **Plan 06 (media)** shipped: `POST /v1/media` (multipart/form-data) sends image + document outbound; `GET /v1/media/{message_id}` streams stored bytes; inbound media events auto-download in a background goroutine for all 5 kinds (image, video, audio, document, sticker). Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>` (content-addressable). Body cap configurable via `[http] max_body_bytes` (default 100 MiB).
- **Plan 07a (replies + edits + deletes)** shipped: `POST /v1/messages` accepts `reply_to`; `PATCH /v1/messages/{id}` edits an outbound message (owner-only, 403 otherwise); `DELETE /v1/messages/{id}` revokes via whatsmeow's REVOKE ProtocolMessage. Inbound REVOKE / MESSAGE_EDIT events from whatsmeow update local rows (`deleted_at`, `body` + `edited_at`).
- **Plan 07b (reactions)** shipped: `POST /v1/messages/{id}/reactions {emoji}` adds or clears (empty emoji) a reaction; `GET /v1/messages/{id}/reactions` lists all reactions for a message. New `reactions` table (FK-cascade with messages). Inbound reaction events auto-persist.
- **Plan 07c (read receipts + typing)** shipped: `POST /v1/messages/{id}/read` marks a received message as read (decrements `chats.unread_count`); `POST /v1/chats/{jid}/typing {state}` sends `composing`/`paused` presence; `GET /v1/messages/{id}/receipts` lists who has acked the message. New `receipts` table populated from inbound `events.Receipt`.
- **Plan 08 (groups)** shipped: `POST /v1/groups` creates a group (chat row upserted with `kind=group`); `GET /v1/groups/{jid}/members` lists members live; `POST /v1/groups/{jid}/members` adds or removes members (returns per-JID outcomes); `DELETE /v1/groups/{jid}/membership` leaves the group (history preserved). All four use whatsmeow's group APIs directly — no schema changes.
- **Plan 09 (SSE event stream)** shipped: `GET /v1/events` emits a Server-Sent-Events stream of inbound events (`message.received`, `message.edited`, `message.deleted`, `reaction.received`, `receipt.received`) plus `connection.state` transitions. Resume is supported via the standard `Last-Event-ID` header (or `?since=<seq>`) backed by the existing `events_log` table; on every reconnect a synthetic `connection.state` frame at id 0 reflects the daemon's current state. Per-subscriber buffer (`[http] sse_subscriber_buffer`, default 256) drops slow readers with a terminal `event: error` frame; heartbeat interval configurable via `[http] sse_heartbeat_seconds` (default 25s). Payloads carry `"v": 1` for forward compatibility.
- **Plan 10 (Postgres store)** shipped: the daemon now runs against either SQLite (default, dev) or Postgres (production), selected via `[storage] backend = "sqlite" | "postgres"` and `postgres_dsn`. Schema and queries are dialect-specific (`internal/store/sqlite/` and `internal/store/postgres/`); a shared test suite (`internal/store/storesuite/`) runs the same assertions against both backends so dialect drift surfaces in tests. Full-text search uses FTS5 on SQLite and `tsvector` + GIN on Postgres — same `/v1/messages/search` contract, dialect-specific ranking. Postgres tests use testcontainers-go (`postgres:16-alpine`) and skip cleanly when Docker is unavailable.
- **Plan 11 (Docker + CI + examples)** shipped: image published to `ghcr.io/askarzh/whatsmeow-api` on every push to main and on `v*` tags; GitHub Actions CI runs `golangci-lint` + `go test -race` (both dialects via testcontainers) on every PR; `examples/docker-compose/` provides a self-host setup with sqlite + postgres profiles; `examples/cookbook.md` documents every HTTP endpoint with copy-pasteable curl recipes.
- **Plan 12 (MCP server transport)** shipped: streamable-HTTP MCP transport at `POST/GET /v1/mcp` mounted on the daemon, gated by `WMAPI_MCP__ENABLED` (default on) and reusing the existing bearer-token middleware. 25 tools cover the full REST surface (`wa_status`, `wa_send_text`, `wa_send_media`, `wa_list_chats`, `wa_search_messages`, group ops, login, …) and call directly into `service.Service` — no double-hop through REST. Built on the official `github.com/modelcontextprotocol/go-sdk`.

v1 is complete and MCP is shipped. Future work — outbound message lifecycle events, group-lifecycle deltas, video/audio/sticker outbound, multi-arch image, helm chart — is tracked in the spec backlog and lives in follow-up plans as real consumer needs surface.

## Quick start

```bash
make build
./bin/whatsmeow-api serve

# in another terminal:
./bin/whatsmeow-api login qr        # scan with WhatsApp on your phone
./bin/whatsmeow-api status          # confirm pairing
```

`login phone +27821234567` is the alternative if you can't scan a QR — the daemon prints an 8-character code; enter it on the linked-device screen.

## Run with Docker

Pull the latest dev image and run against SQLite:

```bash
docker run --rm -p 8080:8080 \
           -v $(pwd)/data:/data \
           -e WMAPI_AUTH__TOKEN=change-me \
           -e WMAPI_SERVER__BIND=0.0.0.0 \
           ghcr.io/askarzh/whatsmeow-api:main
```

For a self-host setup with Postgres, see [`examples/docker-compose/`](examples/docker-compose/).

For a curl recipe per endpoint, see [`examples/cookbook.md`](examples/cookbook.md).

## Connect from Claude

The daemon speaks MCP over streamable HTTP at `/v1/mcp`. Claude Code, Claude Desktop, and claude.ai can drive every capability through 25 typed tools (`wa_status`, `wa_send_text`, `wa_list_chats`, `wa_search_messages`, …) — Claude calls into `service.Service` directly through the same daemon process, no double-hop through REST. The endpoint reuses the existing bearer-token middleware.

See [`examples/claude-mcp/`](examples/claude-mcp/) for a copy-pasteable setup. The full tool catalog lives in [Plan 12's spec](docs/superpowers/specs/2026-05-13-whatsmeow-api-12-mcp-server-design.md).

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

# whatsmeow-api

A long-running HTTP/SSE daemon that wraps [`whatsmeow`](https://github.com/tulir/whatsmeow) and exposes a stable JSON API for a single WhatsApp account. Designed primarily as a backend for an MCP server, but usable from any HTTP client.

## Status

- **Plan 01 (Foundations)** shipped: daemon boots, loads config, logs structured output, serves `/v1/health` and `/v1/status`.
- **Plan 02 (waclient + login)** shipped: real WhatsApp connection via whatsmeow, SSE-driven QR + phone-pair login (`/v1/login/qr`, `/v1/login/phone`), `/v1/logout`, auto-resume on startup, and CLI subcommands (`login qr`, `login phone <number>`, `status`, `logout`) that drive the daemon over its own API.
- **Plan 03 (app store)** shipped: SQLite-backed persistence layer with seven tables (`chats`, `messages`, `messages_fts`, `contacts`, `media`, `events_log`, `kv`) and `golang-migrate`-driven schema migrations that auto-run on `serve`.
- **Plan 04 (send + receive)** shipped: `POST /v1/messages` sends a text message via whatsmeow and persists the outbound row. Inbound message events from whatsmeow are persisted automatically (text + media kinds; media metadata lands in Plan 06). `chats.last_msg_at`, `chats.unread_count`, and `contacts.push_name` update in real time.
- **Plan 05 (list + search)** shipped: read-side endpoints over the app store. `GET /v1/chats` (cursor pagination), `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages` (cursor pagination), `GET /v1/messages/search?q=`, `GET /v1/contacts`, `GET /v1/contacts/search?q=`, `GET /v1/stats`.
- **Plan 06 (media)** shipped: `POST /v1/media` (multipart/form-data) sends image + document outbound; `GET /v1/media/{message_id}` streams stored bytes; inbound media events auto-download in a background goroutine for all 5 kinds (image, video, audio, document, sticker). Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>` (content-addressable). Body cap configurable via `[http] max_body_bytes` (default 100 MiB).

Reactions / replies / edits / deletes / read receipts land in Plan 07; SSE event stream in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.

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

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

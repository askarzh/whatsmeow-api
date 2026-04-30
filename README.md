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

If `auth.token` is empty the daemon refuses to start unless it binds to a loopback address (e.g. `127.0.0.1` or `::1`). Set `auth.token` whenever you bind elsewhere.

## Layout

See `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md` for the full design.

## License

TBD

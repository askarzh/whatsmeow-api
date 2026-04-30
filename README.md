# whatsmeow-api

A long-running HTTP/SSE daemon that wraps [`whatsmeow`](https://github.com/tulir/whatsmeow) and exposes a stable JSON API for a single WhatsApp account. Designed primarily as a backend for an MCP server, but usable from any HTTP client.

## Status

Early scaffold. See `docs/superpowers/specs/` for the design.

## Quick start

```bash
go run ./cmd/whatsmeow-api serve
```

## Configuration

`./config.toml` (or `WMAPI_*` env vars). See `docs/superpowers/specs/` for the full key list.

## License

TBD

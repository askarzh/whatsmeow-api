# Connect Claude to whatsmeow-api

Three steps.

## 1. Start the daemon

The daemon must bind a port reachable from your MCP host. Localhost is enough for Claude Code; for Claude Desktop or claude.ai, expose it via a tunnel (cloudflared, ngrok) or a reverse proxy.

```sh
docker run --rm \
  -p 8080:8080 \
  -e WMAPI_AUTH__TOKEN=$(openssl rand -hex 16) \
  -e WMAPI_SERVER__BIND=0.0.0.0 \
  -v wm-data:/data \
  ghcr.io/askarzh/whatsmeow-api:main
```

Note the token printed in the daemon's logs — you'll need it below.

## 2. Pair (one-time)

If you haven't already paired this WhatsApp account with the daemon, sign in via `wa_login_qr` or `wa_login_phone` once the MCP server is connected. The REST cookbook at [`examples/cookbook.md`](../cookbook.md) covers the same flow over HTTP.

## 3. Add the MCP server to Claude

### Claude Code

Copy [`claude_code_config.json`](claude_code_config.json) into your project's `.mcp.json` (or merge into an existing one), and either:

- export `WMAPI_AUTH_TOKEN=<your-token>` in your shell before launching `claude`, or
- replace `${WMAPI_AUTH_TOKEN}` in the JSON with the literal token.

Re-open the project; Claude Code prompts for trust on first use.

### Claude Desktop / claude.ai

Settings → Connectors → Add custom connector → paste the daemon's public URL (e.g. `https://your-domain/v1/mcp`) and the bearer token. claude.ai and Desktop don't accept localhost; tunnel the daemon (cloudflared, ngrok) or stand it up behind a reverse proxy.

## 4. Try it

Ask Claude:

> "What's my WhatsApp status?"

Claude calls `wa_status` and reports back. The full tool catalog (25 tools across status, login, send/receive, search, contacts, groups, media) is in the [Plan 12 spec](../../docs/superpowers/specs/2026-05-13-whatsmeow-api-12-mcp-server-design.md).

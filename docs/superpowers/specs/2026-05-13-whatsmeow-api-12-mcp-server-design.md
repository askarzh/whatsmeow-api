# whatsmeow-api Plan 12 — MCP Server Transport

**Date:** 2026-05-13
**Status:** Design — ready for implementation plan
**Predecessors:** Plans 01–11 shipped on `main`. v1 milestone complete. This plan is the first post-v1 capability.

## 1. Goals

Expose the daemon's existing capabilities as an MCP server so that Claude (Claude Code, Claude desktop, claude.ai with a tunnel/reverse proxy, or any other MCP host) can drive the WhatsApp account through structured tools instead of bespoke HTTP calls.

Concretely:

- The daemon binds a streamable-HTTP MCP transport at `POST /v1/mcp` and `GET /v1/mcp` (per the MCP spec's streamable-HTTP transport).
- One MCP tool per HTTP action — ~25 tools — backed by direct calls into the existing `service.Service` interface (no double-hop through the REST API).
- Reuses the daemon's bearer-token middleware; `WMAPI_AUTH__TOKEN` controls access for both REST and MCP.
- Ships with the existing Docker image and `serve` subcommand; no new binary, no new config beyond a single `mcp.enabled` toggle (default on).
- Has equivalent test coverage to the HTTP handlers: in-process MCP client driving the streamable-HTTP handler, happy-path + error-mapping cases.

## 2. Non-goals

- **MCP-over-stdio shim binary.** A `cmd/whatsmeow-mcp` shim that proxies HTTP from a subprocess is appealing for Claude Code installs that can't tunnel to the daemon, but the daemon is already reachable from Claude Code on localhost, and other consumers (Claude desktop, claude.ai) reach it over HTTP anyway. Stdio shim can land as a follow-up if a real consumer needs it.
- **MCP app widgets, elicitation, sampling, prompts, resources.** Plain tools returning text/JSON only. Once the surface stabilises we can revisit elicitation for things like group-name confirmation, and consider resources for media (`wa://media/<message-id>`).
- **SSE event stream as MCP notifications.** The daemon's `GET /v1/events` stays as the live channel. Re-publishing the same stream through MCP notifications doubles the surface for no obvious benefit; if a consumer needs it we add it then.
- **OAuth / DCR / CIMD.** Bearer token only. The daemon is single-tenant and operator-owned; OAuth is only worth building once a multi-user hosted offering exists.
- **A per-tool rate limiter, per-tool audit log, multi-tenancy.** Out of scope; the existing daemon-wide log + bearer auth cover the threat model.
- **Search + execute pattern.** The action surface is ~25 — comfortably inside the one-tool-per-action sweet spot. Schemas land in Claude's context cleanly without flooding it.
- **Submission to the Anthropic Connector Directory.** That's a packaging/business question, not an engineering one. The transport must work first.

## 3. Architecture

The MCP transport is a sibling of the existing chi-based HTTP transport and SSE handler. All three share the same `service.Service` and the same daemon process:

```
                  ┌────────────────────────────────────────────────┐
                  │ daemon (cmd/whatsmeow-api serve)               │
                  │                                                │
                  │  ┌──────────────┐                              │
   Claude ─MCP────┤  │ /v1/mcp      │  ─┐                          │
                  │  └──────────────┘   │                          │
                  │  ┌──────────────┐   │   ┌──────────────────┐   │
   curl   ─REST───┤  │ /v1/...      │  ─┼─▶ │ service.Service  │   │
                  │  └──────────────┘   │   └────────┬─────────┘   │
                  │  ┌──────────────┐   │            │             │
   curl   ─SSE────┤  │ /v1/events   │  ─┘            ▼             │
                  │  └──────────────┘     ┌──────────────────┐     │
                  │                       │ waclient, stores │     │
                  │                       └──────────────────┘     │
                  └────────────────────────────────────────────────┘
```

New package: `internal/transport/mcp`. Lives next to `internal/transport/http` and `internal/transport/sse`. Wired into the chi router from `internal/daemon` exactly like the HTTP and SSE transports, behind the same auth middleware.

Key design choices:

- **Single process, no double-hop.** The MCP tool functions hold a `service.Service` directly and call methods like `svc.SendText(ctx, chatJID, text, replyTo)`. The REST handlers and the MCP tools share the same use-case layer; there is no internal HTTP loopback.
- **Mounted under `/v1`.** Same versioning prefix as REST so a future v2 of the daemon can ship a v2 MCP surface side-by-side. The MCP path is `/v1/mcp` (single endpoint serving both POST request/response and GET for server-initiated streams, per the spec).
- **Auth via the same middleware.** `RequireBearerToken` wraps the `/v1/mcp` route group alongside the REST group. No new auth code.
- **Tools call into `Service`, not into stores or `waclient` directly.** Validation, persistence, event-log writes, and ownership checks all happen in `Service`; bypassing it would silently skip those.

## 4. Streamable-HTTP transport

Per the MCP 2025-03-26 spec the streamable-HTTP transport runs on a single endpoint that accepts both `POST` (client → server request, optionally with SSE-streamed responses) and `GET` (server → client notifications / resumed responses).

The Go SDK ([`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), official, maintained by the MCP working group + Google) exposes a `mcp.Server` that can be served over streamable HTTP via `mcp.NewStreamableHTTPHandler(server, opts)`. That handler is an `http.Handler`; chi mounts it directly under `/v1/mcp`.

```go
// pseudocode — finalised in the implementation plan
srv := mcp.NewServer(&mcp.Implementation{
    Name:    "whatsmeow-api",
    Version: version,
}, &mcp.ServerOptions{
    Instructions: instructions,
})
RegisterTools(srv, svc)
h := mcp.NewStreamableHTTPHandler(srv, nil)
r.With(httpapi.RequireBearerToken(cfg.Auth.Token)).Mount("/v1/mcp", h)
```

Headers:

- `Authorization: Bearer <token>` (when `auth.token` is set) — checked by existing middleware.
- `Mcp-Session-Id` — managed by the SDK's streamable-HTTP handler.

CORS: the existing CORS middleware (if any) applies. The daemon is not designed for browser callers — clients run server-side or as native apps.

## 5. Tool catalog

One tool per action, namespaced with the `wa_` prefix so the surface stays distinct when this server is loaded alongside other WhatsApp MCPs. All 25 tools below correspond 1:1 to methods on `service.Service`.

| Tool name | Service method | Params | Returns |
|---|---|---|---|
| `wa_status` | `Status` | — | `{status, jid}` |
| `wa_login_qr` | `LoginQR` | — | `{qr_url, expires_at}` (first event from channel) |
| `wa_login_phone` | `LoginPhone` | `phone_number` | `{code, expires_at}` |
| `wa_logout` | `Logout` | — | `{ok: true}` |
| `wa_send_text` | `SendText` | `chat_jid, text, reply_to?` | `Message` |
| `wa_send_media` | `SendMedia` | `chat_jid, kind, body_base64, caption?, mime_type?, filename?` | `Message` |
| `wa_get_media` | `GetMediaRef` | `message_id` | `MediaRef` |
| `wa_edit_message` | `EditMessage` | `message_id, text` | `Message` |
| `wa_delete_message` | `DeleteMessage` | `message_id` | `{ok: true}` |
| `wa_react` | `SendReaction` | `message_id, emoji` | `{ok: true}` |
| `wa_list_reactions` | `ListReactions` | `message_id` | `Reaction[]` |
| `wa_mark_read` | `MarkMessageRead` | `message_id` | `{ok: true}` |
| `wa_typing` | `SendTyping` | `chat_jid, state` | `{ok: true}` |
| `wa_list_receipts` | `ListReceipts` | `message_id` | `Receipt[]` |
| `wa_list_chats` | `ListChats` | `before?, limit?, include_archived?` | `Chat[]` |
| `wa_get_chat` | `GetChat` | `chat_jid` | `Chat` |
| `wa_list_messages` | `ListMessages` | `chat_jid, before?, limit?` | `Message[]` |
| `wa_search_messages` | `SearchMessages` | `query, limit?` | `Message[]` |
| `wa_list_contacts` | `ListContacts` | — | `Contact[]` |
| `wa_search_contacts` | `SearchContacts` | `query, limit?` | `Contact[]` |
| `wa_create_group` | `CreateGroup` | `name, participant_jids` | `Group` |
| `wa_list_group_members` | `ListGroupMembers` | `group_jid` | `GroupMember[]` |
| `wa_update_group_members` | `UpdateGroupMembers` | `group_jid, action, participant_jids` | `ParticipantChange[]` |
| `wa_leave_group` | `LeaveGroup` | `group_jid` | `{ok: true}` |
| `wa_stats` | `Stats` | — | `Stats` |

Notes on the tool catalog:

- **Schemas are derived from existing request/response DTOs** under `internal/transport/http/*.go`. The MCP package introduces no new DTO; it reuses the same JSON shapes as REST so an LLM that has seen one understands the other.
- **Annotations** — every tool sets `OpenWorldHint: true` (the WhatsApp network is the open world) and `ReadOnlyHint` for the list/search/get/status tools. Write tools (send, edit, delete, react, group ops) leave `ReadOnlyHint` false. `DestructiveHint` is set on `wa_logout`, `wa_delete_message`, `wa_leave_group`. `IdempotentHint` is set on the list/get/search/status tools.
- **Login tools** (`wa_login_qr`, `wa_login_phone`) return only the first event from the underlying channel (QR string or pairing code). Subsequent state transitions are visible via the SSE event stream or `wa_status`. This matches what the REST handlers do today.
- **Descriptions** are written for the LLM, not for a human — short imperative, list when to use vs. when not to, identify required vs. optional params. Authoring guidance: `references/tool-design.md` in the build-mcp-server skill.
- **`wa_send_media`** is the one tool whose payload is not pure JSON-friendly. The REST endpoint accepts `multipart/form-data` with a `file` part, which doesn't map cleanly to MCP. For v1 the tool accepts `body_base64` (base64-encoded bytes) only — the LLM avoids large payloads naturally, and small images/documents round-trip fine. Streaming via `file://` paths or `https://` URLs is listed as a follow-up in §13; it requires new code paths in the service layer, not just the MCP wrapper.

## 6. Error mapping

Service methods return either `nil`, `service.ErrInvalidRequest`, `service.ErrForbidden`, or a domain-specific error (e.g. `store.ErrNotFound`). The mapping to MCP is:

| Service error | MCP response |
|---|---|
| `service.ErrInvalidRequest` (wrapped) | `CallToolResult{IsError: true, Content: text("invalid request: <msg>")}` |
| `service.ErrForbidden` (wrapped) | `CallToolResult{IsError: true, Content: text("forbidden: <msg>")}` |
| `store.ErrNotFound` (wrapped) | `CallToolResult{IsError: true, Content: text("not found: <msg>")}` |
| any other non-nil error | JSON-RPC error reply with code `-32603` ("internal error"); the error message is logged in full via slog and the client sees a generic "internal error" string. |

Tools never return raw `error` from the service layer in the MCP content text — that would risk leaking SQL state, file paths, or auth-internal messages. The middleware/tool wrapper centralises this translation so every tool inherits the same behaviour.

Validation that happens **before** the service call (missing required parameter, wrong type) is handled by the SDK's input-schema validation and surfaces as JSON-RPC errors with code `-32602` ("invalid params"). The implementation does not need to duplicate it.

## 7. Server instructions

The streamable-HTTP server advertises `Instructions` in the initialize response — a paragraph the host injects into Claude's system prompt. Concrete content:

> This server controls a single WhatsApp account through the `whatsmeow-api` daemon. All chat and group identifiers are WhatsApp JIDs (e.g. `1234567890@s.whatsapp.net` for users, `12...-...@g.us` for groups). Phone numbers passed to `wa_login_phone` are E.164 without the `+`. Reactions to clear an existing one use `emoji=""`. Messages are searched against the local cache only; remote-only history is not indexed.

Keep it under 100 words. Long instructions waste context on every conversation; short ones force the LLM to learn from tool descriptions instead.

## 8. Config & wiring

New `internal/config` field:

```go
type MCPConfig struct {
    Enabled bool   `koanf:"enabled"`
    // Path is left unconfigurable for v1 — always /v1/mcp.
}
```

Environment: `WMAPI_MCP__ENABLED=true|false`, default `true`. A future flag could add `WMAPI_MCP__PATH` but YAGNI.

Wiring in `internal/daemon`:

```go
// pseudocode
if d.Config.MCP.Enabled {
    h := mcpserver.New(d.Service, d.Logger, version) // returns http.Handler
    r.With(http.RequireBearerToken(d.Config.Auth.Token)).Mount("/v1/mcp", h)
}
```

`mcpserver.New` is the single public symbol the daemon depends on. Everything else (`registerTools`, error-mapping wrapper, schema builders) is unexported.

## 9. Testing strategy

Three layers, mirroring how the REST transport is tested:

**Unit — tool registration & error mapping (no transport).** Build an `mcp.Server` in-memory, register tools against a fake `Service`, drive it through `mcp.Client` connected via an in-memory transport. Assert: every tool listed; happy-path call returns the expected JSON; service errors map to the expected `IsError` payload. ~80% of MCP-package coverage lives here.

**Integration — streamable HTTP transport.** Build a `chi.Router`, mount the MCP handler, start an `httptest.Server`. Drive it with an `mcp.Client` over streamable-HTTP. Assert: initialize succeeds with bearer; missing bearer returns 401 problem+json (existing middleware path); session-id round-trips on a multi-call session.

**End-to-end smoke.** A handful of curl-equivalent table tests against a paired-daemon stub to confirm the wire format matches what a real client (Claude Code) sees. Use the in-tree fake `Service` (already used by HTTP handler tests).

No new test infrastructure required; everything reuses the existing `httptest` + fake-service setup. The Go SDK ships an in-memory transport for the unit layer.

## 10. Dependencies

One new top-level dependency:

```
github.com/modelcontextprotocol/go-sdk v<latest>
```

Indirect deps come along (likely `github.com/google/jsonschema-go` and friends). The implementation plan pins the exact version after `go mod tidy`.

**Why this SDK over `mark3labs/mcp-go`:** the modelcontextprotocol-org SDK is the official one, co-maintained by Google and the MCP spec working group. It tracks the spec faster, has first-class streamable-HTTP support, and is what new MCP server examples target. `mark3labs/mcp-go` predates the official SDK and is now in legacy mode.

## 11. Migration & docs

- README gains a "Connect from Claude" section after "Run with Docker", with two sub-sections: Claude Code (point at `http://localhost:8080/v1/mcp`) and Claude desktop / claude.ai (tunnel via cloudflared or any reverse proxy).
- `examples/cookbook.md` does **not** grow — it's REST-only by intent. A new `examples/claude-mcp/` directory ships a minimal `claude_code_config.json` snippet and a one-page README.
- `docs/superpowers/specs/2026-04-30-whatsmeow-api-design.md` master design doc — strike the phrase "*separate* MCP server" in §0 and §1 and rewrite as "MCP-over-HTTP transport on the daemon". The intent is preserved; the deployment shape changes.

## 12. Risks and open questions

- **SDK churn.** The Go SDK is pre-1.0. We pin a version and accept a quarterly upgrade tax. If the upgrade is invasive, the wrapper in `internal/transport/mcp` is the single place that breaks — the rest of the daemon doesn't know MCP exists.
- **Streamable HTTP support across hosts.** Claude Code supports streamable HTTP. Claude desktop and claude.ai support remote MCP servers via the connector directory; reaching a self-hosted daemon needs a public URL or a tunnel. Documented in the README.
- **MCP session lifetime vs daemon restarts.** The streamable-HTTP transport allocates a session id per client. The Go SDK keeps session state in memory; a daemon restart drops sessions and clients reconnect transparently (same as SSE today).
- **Send-media payload size.** Base64-encoded bytes round-trip cleanly for small images and documents but a 10 MB video eats Claude's context fast. The tool description states an explicit byte cap (we re-use `WMAPI_HTTP__MAX_BODY_BYTES`) and the LLM is told to refuse oversize uploads rather than truncate. The follow-up — `file://` / `https://` source resolution in `service.SendMedia` — is listed in §13.
- **`wa_login_qr` ergonomics.** Returning a single QR string in a tool reply means the LLM has to render it for the user, which they can't do directly. We could ship the QR as an image content block (the MCP spec allows it), but that's a polish ticket — for v1 the LLM hands the URL/string back and the user runs `qrencode -t ANSI` themselves. Plan 11 already covers the QR-render UX in the cookbook.

## 13. Out-of-scope follow-ups (acknowledged, not built)

- MCP-over-stdio shim (`cmd/whatsmeow-mcp`).
- Server-initiated MCP notifications mirroring the SSE event stream.
- Elicitation for destructive actions (confirm `wa_leave_group`, `wa_logout`).
- An MCP resource per media message (`wa://media/{message_id}`).
- `file://` and `https://` source variants on `service.SendMedia` and the matching MCP/REST surfaces — would let `wa_send_media` skip base64 entirely.
- Submission to the Anthropic Connector Directory.
- Per-tool audit log and metrics counters.

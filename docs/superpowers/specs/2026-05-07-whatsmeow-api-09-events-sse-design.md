# whatsmeow-api Plan 09 — SSE Event Stream + Last-Event-ID Resume

**Date:** 2026-05-07
**Status:** Design — ready for implementation plan
**Predecessors:** Plans 01–08 shipped on `main`. The `events_log` table from Plan 03 is the durable substrate.

## 1. Goals

Expose a single Server-Sent-Events endpoint, `GET /v1/events`, that emits a real-time stream of inbound and connection-state events. Reconnecting clients can resume from where they left off via the standard SSE `Last-Event-ID` header (or a `?since=<id>` query string). The stream is the primary push channel for the daemon's MCP-server consumer; clients no longer need to poll `/v1/messages/search` or `/v1/status` to stay current.

Plan 09 v1 covers inbound events (messages, edits, deletes, reactions, receipts, typing) plus connection-state transitions. Outbound message lifecycle events and group-lifecycle deltas are deferred.

## 2. Non-goals

- **Outbound message lifecycle.** A POST to `/v1/messages` already returns the persisted row. Modeling sent → delivered → read as a stream of events that merge with that response is its own design problem and lands in a follow-up.
- **Group lifecycle deltas.** Plan 08 already exposes live group-membership queries. Group create/join/leave/member-change events wait until a real consumer asks for them.
- **Historical replay beyond `events_log`.** We don't synthesize missed events from `messages` or `chats` rows. Clients that fall outside the retention window must re-bootstrap via the existing read endpoints.
- **Server-side filtering** (`?kinds=`). Easy to add later; out of scope here.
- **Retention pruning.** Per the brainstorm, `events_log` grows unbounded for v1. A pruning sweep is a one-line follow-up if disk pressure ever becomes real.

## 3. Architecture

```
┌──────────────────────────┐         ┌─────────────────────────┐
│ whatsmeow.Client (event) │────────▶│ waclient.Adapter        │
└──────────────────────────┘         │  (translateXxx + cb)    │
                                     └────────────┬────────────┘
                                                  │ IncomingX struct
                                                  ▼
                              ┌────────────────────────────────────┐
                              │ service.handleIncoming/Receipt/... │
                              │   1. persist to domain stores      │
                              │   2. emit() → events_log + publish │
                              └─────────────┬──────────────────────┘
                                            │ Event{id, kind, payload}
                                            ▼
                              ┌────────────────────────────────────┐
                              │ sse.Broadcaster (in-process pub/sub)│
                              │   subs: map[id]chan Event          │
                              └─────────────┬──────────────────────┘
                                            │ fan-out
                                            ▼
                              ┌────────────────────────────────────┐
                              │ http.EventsHandler                 │
                              │   replay events_log WHERE id > L   │
                              │   then tail subscriber channel     │
                              │   heartbeats every 25 s            │
                              └────────────────────────────────────┘
```

The service layer is the funnel. Every existing `handle*` method gains one tail step: build the JSON payload, append a row to `events_log`, then publish to the broadcaster. The HTTP handler does replay-then-tail per connection. The broadcaster is in-process only — there's a single daemon, one account, no cross-process fan-out.

## 4. Endpoint contract

### Request

```
GET /v1/events HTTP/1.1
Authorization: Bearer <token>
Accept: text/event-stream
Last-Event-ID: 4271                ← optional; resume from after this id
```

Or equivalently:

```
GET /v1/events?since=4271 HTTP/1.1
```

If both are present, the header wins. If neither is present, the client gets the live tail starting at the next published event (no replay).

### Response

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no              ← nginx hint to disable proxy buffering
```

Body is an SSE stream. Example:

```
:ready

id: 0
event: connection.state
data: {"v":1,"connected":true,"jid":"15551234567@s.whatsapp.net","since":"2026-05-07T12:00:00Z"}

id: 4272
event: message.received
data: {"v":1,"message_id":"3EB0...","chat_jid":"15557654321@s.whatsapp.net","sender_jid":"15557654321@s.whatsapp.net","kind":"text","body":"hello","timestamp":"2026-05-07T12:34:56Z","push_name":"Alice"}

:ping

id: 4273
event: receipt.received
data: {"v":1,"message_ids":["3EB0..."],"chat_jid":"15557654321@s.whatsapp.net","reader_jid":"15557654321@s.whatsapp.net","type":"read","timestamp":"2026-05-07T12:35:01Z"}
```

The opening `:ready` comment forces a flush so the client knows the stream is established. `:ping` comments are written every 25 s to defeat idle proxy timeouts. Comments do not advance `Last-Event-ID`.

The first non-comment frame after `:ready` is always a synthetic `connection.state` event with `id: 0` reflecting the daemon's *current* WA connection state (not whatever was last in `events_log`). This guarantees consumers know where they stand even if their resume id predates the last real `connection.state` row. The synthetic event uses `id: 0` so it never moves the client's resume cursor.

### Error frames

If a client falls too far behind for the broadcaster's per-subscriber buffer (default 256 events), the broadcaster closes that subscriber's channel. The handler emits one terminal frame and returns:

```
event: error
data: {"v":1,"code":"events.lagged","detail":"subscriber buffer overflowed; reconnect with Last-Event-ID"}
```

The client should reconnect with its last seen id; the replay query will catch it back up.

If the auth token is wrong: standard 401 from the existing `RequireBearerToken` middleware before we open the stream.

If `?since=` parses but is non-numeric: 400 `request.invalid`.

### Status codes

| Outcome | Status |
|---|---|
| Stream established | 200 |
| Bad bearer / missing bearer | 401 |
| `?since=` not a non-negative integer | 400 |

## 5. Event types (v1)

Every payload is JSON, snake_case keys, RFC3339 UTC timestamps. Every payload includes `"v": 1` as the first field — once the v1 contract is shipped, we evolve via additive fields under the same `v` until a breaking change forces `"v": 2`.

| `event:` line | When emitted | Payload |
|---|---|---|
| `connection.state` | login success, login failure, logout, disconnect, reconnect | `{v, connected, jid?, since?, reason?}` |
| `message.received` | inbound text or media (existing `service.handleIncoming` IsFromMe=false path) | `{v, message_id, chat_jid, sender_jid, kind, body, timestamp, push_name?, media?}` |
| `message.edited` | inbound MESSAGE_EDIT ProtocolMessage | `{v, message_id, chat_jid, body, edited_at}` |
| `message.deleted` | inbound REVOKE ProtocolMessage | `{v, message_id, chat_jid, deleted_at}` |
| `reaction.received` | inbound reaction (set or clear) | `{v, target_message_id, sender_jid, emoji, timestamp}` (empty `emoji` means clear) |
| `receipt.received` | inbound delivery / read / played receipt | `{v, message_ids, chat_jid, reader_jid, type, timestamp}` |
| `typing.received` | inbound `composing` or `paused` presence | `{v, chat_jid, sender_jid, state, timestamp}` |

Field detail:

- **`message.received.kind`** is `"text" | "image" | "video" | "audio" | "document" | "sticker"`, mirroring the existing `messages.kind` column.
- **`message.received.media`** is included only when `kind ≠ "text"`. Shape: `{ref, mime_type, size, sha256, caption?}` so a consumer can fetch via `GET /v1/media/{message_id}` without a second round trip for metadata.
- **`receipt.received.type`** is `"delivered" | "read" | "played"`, matching the existing `receipts.type` column. Other whatsmeow receipt types are filtered out as they are today.
- **`typing.received.state`** is `"composing" | "paused"`.
- **`connection.state.connected`** is the boolean that drives the existing `/v1/status` response.
- **`connection.state.reason`** is set on transitions to `connected=false` (`"logout"`, `"disconnect"`, `"login_failed"`).

Outbound (`IsFromMe=true`) inbound events are filtered exactly as they are in the current `handleIncoming` — no echo, no event, consistent with non-goal §2.

## 6. Persistence

Reuse the `events_log` table from Plan 03. The Go-level interface already exists at `internal/store/store.go`:

```go
type EventLogEntry struct {
    Seq     int64
    Time    time.Time
    Type    string
    Payload string // JSON-encoded
}

type EventsLog interface {
    Append(ctx context.Context, entry EventLogEntry) (int64, error)
    SinceSeq(ctx context.Context, seq int64, limit int) ([]EventLogEntry, error)
}
```

`Append` returns the assigned `Seq`, which is what becomes the SSE `id:` field and the resume cursor. The exact column shape is whatever Plan 03's migration defined — the spec doesn't pin SQL details because the Go interface is the contract.

Each emitted event writes one row: `Type` is the event name (e.g. `"message.received"`), `Payload` is the JSON byte-for-byte that goes out the SSE `data:` line. The `Seq` assigned by the store becomes the SSE id.

Append failures are logged at WARN but do not fail the calling `handle*` method — the domain row was already persisted, and we don't want an events-log hiccup to mask a successful inbound message. The corresponding live broadcast is also skipped on insert failure to keep persistence and broadcast aligned: a connected client will see the next event with a higher seq, and the missing event is reachable only via a future backfill.

The synthetic `connection.state` event sent at subscribe time is *not* persisted — it's a view of current state, not a history entry.

**No migration is added in this plan.** If the store interface needs to grow (e.g. a richer pagination call), the implementation plan adds it; the spec stays the same.

## 7. Concurrency model

`internal/transport/sse/broadcaster.go` (the package directory exists since Plan 02 but is empty):

```go
package sse

type Event struct {
    Seq     int64       // events_log row seq, becomes the SSE id
    Kind    string      // event type, e.g. "message.received"
    Payload []byte      // pre-marshaled JSON
}

type Broadcaster struct {
    mu      sync.RWMutex
    subs    map[uint64]chan Event
    next    uint64
    bufSize int                    // per-subscriber buffer; default 256
}

func New(bufSize int) *Broadcaster
func (b *Broadcaster) Subscribe() (id uint64, ch <-chan Event)
func (b *Broadcaster) Unsubscribe(id uint64)
func (b *Broadcaster) Publish(ev Event)  // non-blocking; closes channel on overflow
```

`Publish` walks `subs` under `mu.RLock()`, attempts a non-blocking send to each subscriber, and on send failure (`default:` branch fires) closes that subscriber's channel and removes it from the map. The handler treats a closed channel as the "lagged" signal and emits the terminal `error` frame.

The service holds a single `*Broadcaster` constructed at `service.New` time. Each `handle*` method calls `broadcaster.Publish` after a successful `EventsLog.Append`.

## 8. Per-connection HTTP flow

`internal/transport/http/events.go`:

1. **Validate auth** — handled by the existing `RequireBearerToken` middleware around the `/v1` group. By the time the handler runs, auth is already OK.
2. **Resolve resume cursor (`lastSeq`).** Read `Last-Event-ID` header first, then fall back to `?since=`. Parse as int64 ≥ 0. Reject malformed values with 400 (`request.invalid`). The default when neither is present is `lastSeq = 0` — replay returns nothing, the client gets the live tail starting at the next published event.
3. **Set SSE response headers** (Content-Type, Cache-Control, Connection, X-Accel-Buffering). Write `:ready\n\n` and call `http.Flusher.Flush()` so the client knows the stream is up before any auth/buffering proxy timeout hits.
4. **Emit the synthetic `connection.state` event** with `id: 0`, derived from the current `service.Status()` snapshot.
5. **Subscribe before replay.** Call `broadcaster.Subscribe()` and capture the channel *before* the events_log query so live events that fire during the replay queue up in the channel buffer.
6. **Replay.** `bundle.Events.SinceSeq(ctx, lastSeq, batchSize)` paginates rows with `seq > lastSeq` (this is the existing `store.EventsLog` method). For each `EventLogEntry` write SSE frames and track the largest seq seen as `lastReplayedSeq`. Loop until a batch returns fewer rows than `batchSize`.
7. **Live tail.** Loop on `select { case ev, ok := <-ch: ... case <-r.Context().Done(): return ... case <-heartbeat.C: write :ping\n\n }`.
   - If `ok == false`, the broadcaster closed our channel for lag → write the terminal `error` frame and return.
   - If `ev.Seq <= lastReplayedSeq`, skip (the event was already in the replay batch — handles the race where a Publish landed during replay).
   - Otherwise write `id: %d\nevent: %s\ndata: %s\n\n` and Flush, update `lastReplayedSeq`.
8. **Cleanup.** `defer broadcaster.Unsubscribe(id)`. The handler's `defer` runs on context cancel, write error, or terminal-frame return.

The heartbeat ticker is 25 s, configurable via `[http] sse_heartbeat_seconds` (default 25). A typical proxy idle timeout is 60 s; 25 s gives us margin without being chatty.

## 9. Wiring

- `service.New(...)` constructor accepts an additional `*sse.Broadcaster` parameter and stores it on `*svc`. The constructor in `cmd/whatsmeow-api/serve.go` builds the broadcaster and passes it to both the service and the HTTP handler.
- `internal/transport/http/router.go` adds one route inside the auth-protected group:
  ```go
  r.Method(http.MethodGet, "/events", EventsHandler(d.Service, d.Broadcaster))
  ```
  The `Dependencies` struct (or whatever the current name is — to confirm at impl time) gains a `Broadcaster *sse.Broadcaster` field.

## 10. Service-layer emission

A new helper in `internal/service/events.go`:

```go
type emitter struct {
    log         store.EventsLog
    broadcaster *sse.Broadcaster
    logger      *slog.Logger
}

func (e *emitter) emit(ctx context.Context, kind string, payload any)
```

`emit` marshals `payload` to JSON, calls `log.Append(ctx, store.EventLogEntry{Type: kind, Payload: payloadJSON, Time: time.Now().UTC()})` to get the assigned seq, and on success calls `broadcaster.Publish(Event{Seq: seq, Kind: kind, Payload: payloadJSON})`. On `Append` error it logs at WARN and skips the publish so the broadcast and the durable record stay aligned.

Existing handlers gain one line each:

```go
// in handleIncoming, after persisting the message row:
s.emit(ctx, "message.received", buildMessageReceivedPayload(...))
```

The `build*Payload` functions live alongside `emit` in `events.go` — they translate a domain struct (e.g. `store.Message`, `waclient.IncomingReceipt`) into the JSON contract from §5. Keeping translation in one file makes the wire contract auditable in one place.

## 11. Test strategy

### Unit tests

- **`broadcaster_test.go`** — happy subscribe + publish + unsubscribe; multiple subscribers see the same events; slow subscriber gets dropped after `bufSize` overflow; unsubscribe is idempotent; Publish after Unsubscribe is a no-op for that subscriber.
- **`events_test.go`** (service layer) — `emit` calls `EventsLog.Append` with the correct kind + payload; `emit` publishes only on Append success; `Append` failure logs but does not panic; `buildMessageReceivedPayload` etc. produce the documented JSON shape (golden tests against fixtures).
- **`service_test.go`** existing tests gain one assertion per `handle*` path: verify the relevant event was emitted (`fakeBroadcaster.Published` capture). This is the cheapest way to lock in "every inbound flow emits an event".

### HTTP tests

- **`events_test.go`** (HTTP layer) — uses `httptest.NewServer` + a fake `service.Service`:
  - **Replay path:** seed events_log with 5 rows, connect with `Last-Event-ID: 2`, assert the response body contains rows 3, 4, 5 in order with the right `id:`/`event:`/`data:` framing.
  - **Live tail path:** connect with no resume, then on the server side call `broadcaster.Publish`, assert the client receives the frame.
  - **Heartbeat:** mock the ticker, assert `:ping\n\n` lands.
  - **Lagged subscriber:** fill the broadcaster buffer past `bufSize`, assert the terminal `error` frame.
  - **Synthetic connection.state:** connect, assert the first non-comment frame is `event: connection.state\nid: 0\n` reflecting the fake's current `Status()` value.
  - **Bad `?since=`:** `?since=abc` → 400 `request.invalid`.
  - **Malformed Last-Event-ID:** `Last-Event-ID: abc` → 400.

Pattern matches existing handler tests; `fakeEventsSvc` will need to implement `service.Service` (the existing interface gains no new method — `EventsLog` is queried via the bundle). The test fakes must add the broadcaster reference.

### E2E smoke (Task in plan)

- Start daemon, `curl -N -H "Accept: text/event-stream" http://127.0.0.1:8080/v1/events` → receive `:ready` and `connection.state` immediately.
- With a paired account, send a message from the phone → see `message.received` frame.
- Reconnect with `Last-Event-ID: <last id>` → confirm replay of any frames that landed while disconnected.
- Slow-reader simulation: `curl --limit-rate 1` and trigger many publishes → eventually receive `event: error\ndata: {"code":"events.lagged"}`.

## 12. File structure (proposed)

| Path | Action | Responsibility |
|---|---|---|
| `internal/transport/sse/broadcaster.go` | NEW | `Event`, `Broadcaster`, `New`, `Subscribe`, `Unsubscribe`, `Publish` |
| `internal/transport/sse/broadcaster_test.go` | NEW | broadcaster unit tests |
| `internal/service/events.go` | NEW | `emitter`, `emit`, `build*Payload` translators |
| `internal/service/events_test.go` | NEW | `emit` contract + payload golden tests |
| `internal/service/service.go` | MODIFY | constructor accepts broadcaster; `*svc` stores `*emitter`; each `handle*` calls `s.emit(...)` |
| `internal/service/service_test.go` | MODIFY | assert emit per handle* path; `newInMemoryBundle` returns broadcaster too (or test passes `sse.New(...)` directly — to be decided in plan) |
| `internal/transport/http/events.go` | NEW | `EventsHandler` constructor |
| `internal/transport/http/events_test.go` | NEW | HTTP-layer SSE tests |
| `internal/transport/http/router.go` | MODIFY | add `GET /events` route; accept `Broadcaster` in dependency struct |
| `internal/transport/http/<11 existing>_test.go` | MODIFY | bridge test fakes if `service.Service` interface changes (it shouldn't — `emit` is private) |
| `cmd/whatsmeow-api/serve.go` | MODIFY | construct `*sse.Broadcaster` and wire into both service and HTTP deps |
| `internal/config/config.go` | MODIFY (small) | add `[http] sse_heartbeat_seconds` (default 25), `[http] sse_subscriber_buffer` (default 256) |
| `config.example.toml` | MODIFY | document the two knobs |
| `README.md` | MODIFY | Plan 09 status entry |

Open question to resolve in the implementation plan, not now: whether `service.Service` interface needs to expose anything new (e.g. a `CurrentStatus() waclient.Status` method for the synthetic event) or whether the existing `Status(ctx)` is enough. Likely the existing one works.

## 13. Configuration

Two new keys under `[http]`:

| Key | Default | Meaning |
|---|---|---|
| `sse_heartbeat_seconds` | 25 | Comment-frame interval in seconds |
| `sse_subscriber_buffer` | 256 | Per-subscriber channel buffer; overflow drops the connection |

No new top-level section; SSE is a transport-layer detail.

## 14. Risks and trade-offs

1. **Synthetic connection.state with `id: 0`.** Using `0` relies on real `EventLogEntry.Seq` values being ≥ 1, which the store's current implementation guarantees. If that ever changes, the synthetic id needs a different sentinel — or we can simply omit the `id:` line on the synthetic frame, since SSE clients treat a missing id as "do not update the cursor." The plan should add an assertion in the broadcaster smoke that the first real `Seq` is ≥ 1.

2. **Replay/live race.** If a publish lands between rows being written to events_log and the handler's "subscribe before replay" step, we could double-emit. The handler dedupes by skipping `ev.Seq ≤ lastReplayedSeq`. Add an explicit test for this race.

3. **Backpressure interaction with EventsLog.Append.** If `Append` ever becomes slow (it shouldn't — it's a single SQLite insert on a busy connection), it serializes inbound event handling. Acceptable for v1; if we ever batch inbound messages this needs revisiting.

4. **Reconnect storm.** If a misbehaving client opens many SSE connections in a tight loop, each gets a full replay scan over events_log. Replay queries are indexed by primary key (`id > ?`), so they're cheap, but a busy client could still saturate disk I/O. Out of scope to mitigate in v1; the existing `RequireBearerToken` middleware is the gate.

5. **JSON-shape stability.** Once a consumer ships against `v: 1`, breaking changes need `v: 2`. The discipline is: only add fields under `v: 1`; remove/rename → bump version. Document this in the README's Plan 09 entry.

## 15. Open questions for the implementation plan (not blockers for spec)

- Exact existing schema of `events_log` (created_at type, autoincrement on/off). Verify before writing the impl plan.
- Whether `cmd/whatsmeow-api/serve.go` constructs a single broadcaster or one per dependency tree (single is the obvious answer; flagging in case the serve struct shape complicates things).
- The exact existing name of the HTTP `Dependencies` struct in `router.go`. To resolve at plan-writing time.

---

## Approval gate

If approved, the implementation plan slots roughly as:

1. `sse.Broadcaster` + tests (no service/HTTP wiring yet).
2. `service.emitter` + `build*Payload` golden tests.
3. Wire `emit` into one `handle*` (start with `handleIncoming`) end-to-end with one new service test.
4. Wire `emit` into the remaining `handle*` paths.
5. `EventsHandler` HTTP layer with replay + live-tail + heartbeat + lagged-error frames.
6. Router + dependency wiring.
7. Config keys + example TOML.
8. E2E smoke + README.

Estimated 8 tasks. Date: 2026-05-07.

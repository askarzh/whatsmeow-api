# whatsmeow-api Plan 08 — Groups Design

**Date:** 2026-05-07
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 07c (read receipts + typing) — merged.

## 1. Purpose

Four group-operation endpoints — create a group, list its members, add or remove members, leave. All four are live operations against whatsmeow; no new schema. The only side effect on local state is upserting a `chats` row when a new group is created so the chat list reflects it immediately.

## 2. Goals

- Cover the master design's §6.1 group surface: `POST /v1/groups`, `GET /v1/groups/{jid}/members`, `POST /v1/groups/{jid}/members`, `DELETE /v1/groups/{jid}/membership`.
- Live queries — no cache table, no whatsmeow-event-driven sync. `GET .../members` calls `Client.GetGroupInfo` each time. Members can change frequently and Plan 08 has no consumer that needs offline / sub-millisecond reads.
- Add/remove participants is a single endpoint with an `action` field (`"add"` or `"remove"`). Returns per-participant outcomes so callers can see partial success.
- Creating a group upserts the local `chats` row — the chat is immediately visible in `GET /v1/chats`. Leaving a group does NOT delete the row; history stays.
- Validation: enforce WhatsApp's name length cap (25 chars), require ≥ 1 participant on create and on add/remove.
- Group JIDs end in `@g.us`; we trust whatsmeow to surface that. The HTTP layer doesn't pre-validate the JID format.

## 3. Non-goals (Plan 08)

- Group avatar / image upload + change.
- Group description / topic.
- Group settings (announce-only, edit-info-restricted, members-can-add, etc.).
- Group invite links (`POST /v1/groups/{jid}/invite`).
- Promote / demote admins.
- A `group_members` cache table.
- Global `GET /v1/groups` listing — use `GET /v1/chats` filtered by `kind == "group"` client-side.
- Auto-archiving the chat row on leave (history preserved; future plan can add).
- Group-message metadata beyond what Plan 04+ already persists (sender_jid distinguishes group members).

## 4. Architecture

```
POST /v1/groups {name, participants}
        │
        ▼
  service.CreateGroup(name, participantJIDs)
    ├── validate (name non-empty, ≤ 25, ≥ 1 participant)
    ├── wa.CreateGroup(name, participantJIDs)
    └── bundle.Chats.Put({JID, Kind: "group", Name, LastMsgAt: time.Now()})
        → 201 {jid, name, owner_jid, created_at, members}

GET /v1/groups/{jid}/members
        │
        ▼
  service.ListGroupMembers(groupJID)
    ├── validate
    └── wa.GetGroupInfo(groupJID) → return Participants
        → 200 {members: [{jid, is_admin, is_super_admin}, ...]}

POST /v1/groups/{jid}/members {action, participants}
        │
        ▼
  service.UpdateGroupMembers(groupJID, action, participantJIDs)
    ├── validate (action ∈ {add, remove}, ≥ 1 participant)
    └── wa.UpdateGroupParticipants(groupJID, action, participantJIDs)
        → 200 {results: [{jid, ok, error?}, ...]}

DELETE /v1/groups/{jid}/membership
        │
        ▼
  service.LeaveGroup(groupJID)
    ├── validate
    └── wa.LeaveGroup(groupJID)
        → 204
        Local chats row stays in place (history preserved).
```

## 5. Domain types

Added to `internal/waclient/waclient.go`:

```go
type Group struct {
    JID          string
    Name         string
    OwnerJID     string
    CreatedAt    time.Time
    Participants []GroupMember
}

type GroupMember struct {
    JID          string
    IsAdmin      bool
    IsSuperAdmin bool
}

// ParticipantChange describes the per-JID outcome of an add/remove batch.
// Empty Error means OK == true.
type ParticipantChange struct {
    JID   string
    OK    bool
    Error string
}
```

## 6. WAClient interface

```go
// internal/waclient/waclient.go

CreateGroup(ctx context.Context, name string, participantJIDs []string) (Group, error)
GetGroupInfo(ctx context.Context, groupJID string) (Group, error)
UpdateGroupParticipants(ctx context.Context, groupJID, action string, participantJIDs []string) ([]ParticipantChange, error)
LeaveGroup(ctx context.Context, groupJID string) error
```

`action` is `"add"` or `"remove"`. Other values return `fmt.Errorf("unsupported action: %q", action)` from the adapter.

## 7. Adapter implementation

`CreateGroup`:
1. Connection check → `ErrNotConnected`.
2. Parse each participant JID; collect into `[]types.JID`.
3. Build `whatsmeow.ReqCreateGroup{Name: name, Participants: parsedJIDs}`.
4. `info, err := client.CreateGroup(ctx, req)`.
5. Translate `*types.GroupInfo` → `Group` via `translateGroup(info)` helper.
6. Return.

`GetGroupInfo`:
1. Connection check.
2. `parsedJID, err := types.ParseJID(groupJID)`.
3. `info, err := client.GetGroupInfo(parsedJID)`.
4. Translate.

`UpdateGroupParticipants`:
1. Connection check.
2. Map action: `"add"` → `whatsmeow.ParticipantChangeAdd`, `"remove"` → `whatsmeow.ParticipantChangeRemove`. Other → unsupported error.
3. Parse all participant JIDs.
4. `result, err := client.UpdateGroupParticipants(ctx, groupParsedJID, parsedJIDs, change)`.
5. Translate `[]whatsmeow.GroupParticipant` (or whatever whatsmeow returns) into `[]ParticipantChange`. The whatsmeow result is per-JID; "OK" means `Error == 0`/no error reported, otherwise populate Error string from the whatsmeow error code.

`LeaveGroup`:
1. Connection check.
2. `client.LeaveGroup(parsedJID)`.

`translateGroup(info *types.GroupInfo) Group`:
- `JID`: `info.JID.String()`.
- `Name`: `info.Name`.
- `OwnerJID`: `info.OwnerJID.String()` (or whichever whatsmeow field — verify with `go doc go.mau.fi/whatsmeow/types.GroupInfo`).
- `CreatedAt`: `info.GroupCreated` (time.Time).
- `Participants`: iterate `info.Participants`, building `GroupMember{JID: p.JID.String(), IsAdmin: p.IsAdmin, IsSuperAdmin: p.IsSuperAdmin}`.

> Note for the implementer: whatsmeow's exact `*types.GroupInfo` field names may differ. Run `go doc go.mau.fi/whatsmeow/types.GroupInfo` and adapt. The intent is "translate whatsmeow's group struct into our domain type with stringified JIDs".

## 8. Service layer

```go
type Service interface {
    // existing surface
    CreateGroup(ctx context.Context, name string, participantJIDs []string) (waclient.Group, error)
    ListGroupMembers(ctx context.Context, groupJID string) ([]waclient.GroupMember, error)
    UpdateGroupMembers(ctx context.Context, groupJID, action string, participantJIDs []string) ([]waclient.ParticipantChange, error)
    LeaveGroup(ctx context.Context, groupJID string) error
}
```

`CreateGroup`:
1. Validate `strings.TrimSpace(name) != ""`, `utf8.RuneCountInString(name) <= 25`, `len(participantJIDs) >= 1` → `ErrInvalidRequest`.
2. `group, err := wa.CreateGroup(ctx, name, participantJIDs)`. Bubble errors.
3. Upsert `chats` (read-modify-write to preserve any existing fields):
   ```go
   chat := store.Chat{
       JID: group.JID, Name: group.Name, Kind: "group", LastMsgAt: time.Now(),
   }
   if err := bundle.Chats.Put(ctx, chat); err != nil {
       s.logger.Warn("upsert chat on group create failed", "jid", group.JID, "err", err)
   }
   ```
   Failure logged not propagated.
4. Return `group`.

`ListGroupMembers`:
1. Validate `groupJID != ""`.
2. `group, err := wa.GetGroupInfo(ctx, groupJID)`. Bubble.
3. Return `group.Participants`.

`UpdateGroupMembers`:
1. Validate `groupJID != ""`, `action in {"add","remove"}`, `len(participantJIDs) >= 1` → `ErrInvalidRequest`.
2. Delegate.

`LeaveGroup`:
1. Validate.
2. Delegate. No local-state changes. Failure bubbles.

## 9. HTTP

New file `internal/transport/http/groups.go`. Four handlers + small encoder helpers (`encodeGroup`, `encodeMember`, `encodeChange`).

**Request shapes:**
```jsonc
// POST /v1/groups
{"name": "My Group", "participants": ["27821234567@s.whatsapp.net", "..."]}

// POST /v1/groups/{jid}/members
{"action": "add", "participants": ["..."]}
```

**Response shapes:**
```jsonc
// POST /v1/groups → 201
{
  "jid": "123-456@g.us",
  "name": "My Group",
  "owner_jid": "27821234567@s.whatsapp.net",
  "created_at": "2026-05-07T12:34:56Z",
  "members": [
    {"jid": "...", "is_admin": false, "is_super_admin": false},
    ...
  ]
}

// GET /v1/groups/{jid}/members → 200
{"members": [{"jid": "...", "is_admin": true, "is_super_admin": false}, ...]}

// POST /v1/groups/{jid}/members → 200
{"results": [
  {"jid": "...", "ok": true},
  {"jid": "...", "ok": false, "error": "not in contacts"}
]}

// DELETE /v1/groups/{jid}/membership → 204 (no body)
```

Routes (auth-protected group):
```go
r.Method(http.MethodPost,   "/groups", CreateGroupHandler(d.Service))
r.Method(http.MethodGet,    "/groups/{jid}/members", ListGroupMembersHandler(d.Service))
r.Method(http.MethodPost,   "/groups/{jid}/members", UpdateGroupMembersHandler(d.Service))
r.Method(http.MethodDelete, "/groups/{jid}/membership", LeaveGroupHandler(d.Service))
```

Status mapping (each handler):
- 200 / 201 / 204 success
- 400 `request.invalid` — bad JSON, missing name, empty participants, bad action, name too long
- 409 `wa.not_connected` — `errors.Is(err, waclient.ErrNotConnected)`
- 500 default — anything else from waclient or service

We don't synthesize a 404 for "group doesn't exist / I'm not in it" because whatsmeow's exact error shape varies; surfacing 500 with the wrapped error is acceptable for v1. A consumer who sees a 500 from `GET /v1/groups/{jid}/members` can interpret "the group is unknown" from context.

## 10. Wiring

`cmd/whatsmeow-api/serve.go` is unchanged — Service interface grows; constructor wiring is unaffected.

The 10 existing HTTP fake services (status, login_qr, login_phone, logout, messages, chats, contacts, stats, media, reactions, receipts) need stubs for the 4 new Service methods so `var _ service.Service = ...` checks compile. Same bridge pattern as Plan 07a/b/c.

## 11. Testing strategy

**No automated tests for the adapter** — real WhatsApp dependency. Manual smoke covers it.

**Service** (`internal/service/service_test.go`):
- TestCreateGroupHappyPath — fake WA returns Group; verify `chats` row upserted with `kind == "group"` and the right name.
- TestCreateGroupValidation — empty name, name with > 25 runes, empty participants → `ErrInvalidRequest`.
- TestCreateGroupNotConnected — fake returns `ErrNotConnected`; chats row NOT touched.
- TestListGroupMembersHappyPath — fake returns Group with 3 participants; service returns the slice.
- TestListGroupMembersValidation — empty groupJID.
- TestListGroupMembersNotConnected.
- TestUpdateGroupMembersHappyPath — fake captures action + participants; returns canned `[]ParticipantChange`.
- TestUpdateGroupMembersValidation — bad action, empty participants.
- TestUpdateGroupMembersNotConnected.
- TestLeaveGroupHappyPath — fake captures groupJID; assert local `chats` row is unchanged after.
- TestLeaveGroupValidation, TestLeaveGroupNotConnected.

**HTTP** (`internal/transport/http/groups_test.go`):
- TestCreateGroupHappyPath — POST returns 201; JSON shape matches.
- TestCreateGroupBadJSON, TestCreateGroupNotConnected.
- TestListGroupMembersHappyPath — GET returns 200 with members array.
- TestListGroupMembersNotConnected.
- TestUpdateGroupMembersHappyPath — POST with action=add/remove; results array.
- TestUpdateGroupMembersBadJSON, TestUpdateGroupMembersBadAction.
- TestLeaveGroupHappyPath — DELETE returns 204.
- TestLeaveGroupNotConnected.

## 12. File layout

```
internal/waclient/
  waclient.go              +Group, +GroupMember, +ParticipantChange; +4 interface methods
  whatsmeow_adapter.go     +CreateGroup, +GetGroupInfo, +UpdateGroupParticipants, +LeaveGroup;
                            +translateGroup helper

internal/service/
  service.go               +CreateGroup, +ListGroupMembers, +UpdateGroupMembers, +LeaveGroup
  service_test.go          new tests; fake WA gets 4 captures; bridge HTTP fakes for 4 new Service methods

internal/transport/http/
  groups.go (new)          4 handlers + encodeGroup/encodeMember/encodeChange helpers
  groups_test.go (new)
  router.go                +4 routes

README.md                  status section
```

No schema migrations. No new dependencies.

## 13. Dependencies

None added. `Client.CreateGroup`, `Client.GetGroupInfo`, `Client.UpdateGroupParticipants`, `Client.LeaveGroup` are all in whatsmeow.

## 14. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new service + HTTP tests.
- Manual smoke against paired account:
  - Create group: `curl -X POST -d '{"name":"Test","participants":["<jid>"]}' .../v1/groups` → 201 with full group info; recipient's WhatsApp shows the new group.
  - List members: `curl .../v1/groups/<group_jid>/members` → 200.
  - Add/remove: `curl -X POST -d '{"action":"add","participants":["<jid>"]}' .../v1/groups/<group_jid>/members` → 200; same for remove.
  - Leave: `curl -X DELETE .../v1/groups/<group_jid>/membership` → 204.
- Existing Plan 01–07 endpoints unchanged.

## 15. Open questions deferred to implementation

- Whatsmeow's exact result shape for `Client.UpdateGroupParticipants`. The implementer runs `go doc go.mau.fi/whatsmeow.Client.UpdateGroupParticipants` and adapts the translation.
- Whatsmeow's `*types.GroupInfo.GroupCreated` vs `CreationTime` vs another field name — verify and adapt.
- Whether `Client.CreateGroup` returns `(*types.GroupInfo, error)` or some other type — verify and adapt.
- Whether name validation should also reject leading/trailing whitespace. v1 trims-then-checks-length; if WhatsApp rejects, the error surfaces as 500. Acceptable.

# whatsmeow-api Plan 08 — Groups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Four group-operation endpoints — `POST /v1/groups`, `GET /v1/groups/{jid}/members`, `POST /v1/groups/{jid}/members`, `DELETE /v1/groups/{jid}/membership`. Live whatsmeow queries; no schema changes. Creating a group upserts the local `chats` row.

**Architecture:** waclient gains four interface methods + three new domain types (`Group`, `GroupMember`, `ParticipantChange`). Adapter delegates to whatsmeow's `Client.CreateGroup`, `Client.GetGroupInfo`, `Client.UpdateGroupParticipants`, `Client.LeaveGroup`. Service composes them and upserts the chats row on group creation. HTTP layer is four thin handlers in a new `groups.go`.

**Tech Stack:**
- Go 1.26
- Plan 01–07c stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)
- whatsmeow `Client.CreateGroup`, `Client.GetGroupInfo`, `Client.UpdateGroupParticipants`, `Client.LeaveGroup`, `*types.GroupInfo`

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/waclient/waclient.go` | Modified — `+Group`, `+GroupMember`, `+ParticipantChange`; +4 interface methods. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — 4 method impls + `translateGroup` helper. |
| `internal/service/service.go` | Modified — 4 service methods (`CreateGroup`, `ListGroupMembers`, `UpdateGroupMembers`, `LeaveGroup`). |
| `internal/service/service_test.go` | Modified — fake WA captures + 11 tests. |
| `internal/transport/http/groups.go` | NEW — 4 handlers + small encoder helpers. |
| `internal/transport/http/groups_test.go` | NEW. |
| `internal/transport/http/router.go` | Modified — +4 routes. |
| Existing 11 HTTP fakes | Modified — bridge stubs for the 4 new Service methods. |
| `README.md` | Modified — status section. |

No schema migrations. No new dependencies.

---

## Task 1: waclient types + interface extension + adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service_test.go` (fakeWA stubs)

- [ ] **Step 1: Add domain types and interface methods**

Edit `internal/waclient/waclient.go`. Add the new types near the existing domain types (`Status`, `IncomingMessage`, `Sent`, etc.):

```go
// Group is the daemon's view of a WhatsApp group.
type Group struct {
	JID          string
	Name         string
	OwnerJID     string
	CreatedAt    time.Time
	Participants []GroupMember
}

// GroupMember is one participant in a group.
type GroupMember struct {
	JID          string
	IsAdmin      bool
	IsSuperAdmin bool
}

// ParticipantChange describes the per-JID outcome of an add/remove batch.
// OK == true with empty Error means the change applied; OK == false with
// non-empty Error means whatsmeow rejected this specific JID.
type ParticipantChange struct {
	JID   string
	OK    bool
	Error string
}
```

Append to the `WAClient` interface:
```go
// Plan 08
CreateGroup(ctx context.Context, name string, participantJIDs []string) (Group, error)
GetGroupInfo(ctx context.Context, groupJID string) (Group, error)
UpdateGroupParticipants(ctx context.Context, groupJID, action string, participantJIDs []string) ([]ParticipantChange, error)
LeaveGroup(ctx context.Context, groupJID string) error
```

- [ ] **Step 2: Add adapter stubs**

Edit `internal/waclient/whatsmeow_adapter.go`. Insert before `var _ WAClient = (*Adapter)(nil)`:

```go
// CreateGroup is implemented in Plan 08 Task 2.
func (a *Adapter) CreateGroup(ctx context.Context, name string, participantJIDs []string) (Group, error) {
	_ = ctx; _ = name; _ = participantJIDs
	return Group{}, errors.New("waclient: CreateGroup not yet implemented")
}

// GetGroupInfo is implemented in Plan 08 Task 2.
func (a *Adapter) GetGroupInfo(ctx context.Context, groupJID string) (Group, error) {
	_ = ctx; _ = groupJID
	return Group{}, errors.New("waclient: GetGroupInfo not yet implemented")
}

// UpdateGroupParticipants is implemented in Plan 08 Task 2.
func (a *Adapter) UpdateGroupParticipants(ctx context.Context, groupJID, action string, participantJIDs []string) ([]ParticipantChange, error) {
	_ = ctx; _ = groupJID; _ = action; _ = participantJIDs
	return nil, errors.New("waclient: UpdateGroupParticipants not yet implemented")
}

// LeaveGroup is implemented in Plan 08 Task 2.
func (a *Adapter) LeaveGroup(ctx context.Context, groupJID string) error {
	_ = ctx; _ = groupJID
	return errors.New("waclient: LeaveGroup not yet implemented")
}
```

Add `"errors"` import if not present.

- [ ] **Step 3: Bridge fakeWA**

Edit `internal/service/service_test.go`. Add to `fakeWA`:
```go
func (f *fakeWA) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeWA) GetGroupInfo(context.Context, string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeWA) UpdateGroupParticipants(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f *fakeWA) LeaveGroup(context.Context, string) error { return nil }
```

- [ ] **Step 4: Build and test**

```bash
cd /home/askar/src/whatsmeow-api
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. The service tests still call `service.New(...)` which constructs an `*svc`, which depends on the WAClient interface — and the interface is now extended; fakeWA satisfies it via the four new stubs.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/whatsmeow_adapter.go internal/service/service_test.go
git commit -m "waclient: Group + GroupMember + ParticipantChange types + 4 interface methods (stubs)"
```

---

## Task 2: waclient adapter implementations

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test (real WhatsApp). Manual smoke (Task 6) covers it.

- [ ] **Step 1: Inspect the whatsmeow API**

```bash
go doc go.mau.fi/whatsmeow.Client.CreateGroup
go doc go.mau.fi/whatsmeow.ReqCreateGroup
go doc go.mau.fi/whatsmeow.Client.GetGroupInfo
go doc go.mau.fi/whatsmeow.Client.UpdateGroupParticipants
go doc go.mau.fi/whatsmeow.Client.LeaveGroup
go doc go.mau.fi/whatsmeow.ParticipantChange
go doc go.mau.fi/whatsmeow/types.GroupInfo
go doc go.mau.fi/whatsmeow/types.GroupParticipant
```

Confirm:
- `Client.CreateGroup(req ReqCreateGroup) (*types.GroupInfo, error)` — or similar.
- `Client.GetGroupInfo(jid types.JID) (*types.GroupInfo, error)`.
- `Client.UpdateGroupParticipants(jid types.JID, jids []types.JID, action ParticipantChange) ([]types.GroupParticipant, error)` — or similar.
- `Client.LeaveGroup(jid types.JID) error`.
- Constants: `ParticipantChangeAdd`, `ParticipantChangeRemove`.
- `types.GroupInfo` has `JID`, `OwnerJID`, `Name` (or `GroupName`), `GroupCreated` (time.Time), `Participants` ([]types.GroupParticipant).
- `types.GroupParticipant` has `JID`, `IsAdmin`, `IsSuperAdmin`.

If any names differ, adapt — the intent is documented in the steps below.

- [ ] **Step 2: Replace CreateGroup stub**

Replace the stub with:
```go
// CreateGroup creates a new group with the given name and participants. The
// daemon's own JID is implicitly added as super-admin.
func (a *Adapter) CreateGroup(ctx context.Context, name string, participantJIDs []string) (Group, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Group{}, ErrNotConnected
	}
	client := a.client
	a.mu.Unlock()

	parsedJIDs := make([]types.JID, 0, len(participantJIDs))
	for _, jid := range participantJIDs {
		p, err := types.ParseJID(jid)
		if err != nil {
			return Group{}, fmt.Errorf("parse participant_jid %q: %w", jid, err)
		}
		parsedJIDs = append(parsedJIDs, p)
	}

	req := whatsmeow.ReqCreateGroup{Name: name, Participants: parsedJIDs}
	info, err := client.CreateGroup(ctx, req)
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	return translateGroup(info), nil
}
```

> Note: if `Client.CreateGroup` doesn't take a `ctx` parameter, drop it. If `ReqCreateGroup` has different field names (e.g. `GroupName`), adapt.

- [ ] **Step 3: Replace GetGroupInfo stub**

```go
// GetGroupInfo fetches the current group state from whatsmeow.
func (a *Adapter) GetGroupInfo(ctx context.Context, groupJID string) (Group, error) {
	_ = ctx
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Group{}, ErrNotConnected
	}
	client := a.client
	a.mu.Unlock()

	parsed, err := types.ParseJID(groupJID)
	if err != nil {
		return Group{}, fmt.Errorf("parse group_jid: %w", err)
	}
	info, err := client.GetGroupInfo(parsed)
	if err != nil {
		return Group{}, fmt.Errorf("get group info: %w", err)
	}
	return translateGroup(info), nil
}
```

- [ ] **Step 4: Replace UpdateGroupParticipants stub**

```go
// UpdateGroupParticipants adds or removes members from a group.
func (a *Adapter) UpdateGroupParticipants(ctx context.Context, groupJID, action string, participantJIDs []string) ([]ParticipantChange, error) {
	_ = ctx
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrNotConnected
	}
	client := a.client
	a.mu.Unlock()

	groupParsed, err := types.ParseJID(groupJID)
	if err != nil {
		return nil, fmt.Errorf("parse group_jid: %w", err)
	}

	var change whatsmeow.ParticipantChange
	switch action {
	case "add":
		change = whatsmeow.ParticipantChangeAdd
	case "remove":
		change = whatsmeow.ParticipantChangeRemove
	default:
		return nil, fmt.Errorf("unsupported action: %q", action)
	}

	parsedJIDs := make([]types.JID, 0, len(participantJIDs))
	for _, jid := range participantJIDs {
		p, err := types.ParseJID(jid)
		if err != nil {
			return nil, fmt.Errorf("parse participant_jid %q: %w", jid, err)
		}
		parsedJIDs = append(parsedJIDs, p)
	}

	results, err := client.UpdateGroupParticipants(groupParsed, parsedJIDs, change)
	if err != nil {
		return nil, fmt.Errorf("update group participants: %w", err)
	}

	out := make([]ParticipantChange, 0, len(results))
	for _, r := range results {
		change := ParticipantChange{
			JID: r.JID.String(),
		}
		if r.Error == 0 {
			change.OK = true
		} else {
			change.OK = false
			change.Error = fmt.Sprintf("error code %d", r.Error)
		}
		out = append(out, change)
	}
	return out, nil
}
```

> Note: if whatsmeow's `Client.UpdateGroupParticipants` returns `[]types.GroupParticipant` instead, the field with the error code may be named differently (e.g. `r.AddRequest.Code` or absent entirely). Run `go doc` and adapt — the goal is "build a per-JID OK/Error pair from the whatsmeow result".

- [ ] **Step 5: Replace LeaveGroup stub**

```go
// LeaveGroup leaves the given group.
func (a *Adapter) LeaveGroup(ctx context.Context, groupJID string) error {
	_ = ctx
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return ErrNotConnected
	}
	client := a.client
	a.mu.Unlock()

	parsed, err := types.ParseJID(groupJID)
	if err != nil {
		return fmt.Errorf("parse group_jid: %w", err)
	}
	if err := client.LeaveGroup(parsed); err != nil {
		return fmt.Errorf("leave group: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Add the translateGroup helper**

Append at the bottom of the file (or next to other translate* helpers):
```go
// translateGroup maps whatsmeow's *types.GroupInfo into our domain Group.
func translateGroup(info *types.GroupInfo) Group {
	if info == nil {
		return Group{}
	}
	participants := make([]GroupMember, 0, len(info.Participants))
	for _, p := range info.Participants {
		participants = append(participants, GroupMember{
			JID:          p.JID.String(),
			IsAdmin:      p.IsAdmin,
			IsSuperAdmin: p.IsSuperAdmin,
		})
	}
	return Group{
		JID:          info.JID.String(),
		Name:         info.Name,
		OwnerJID:     info.OwnerJID.String(),
		CreatedAt:    info.GroupCreated,
		Participants: participants,
	}
}
```

> Note: if the whatsmeow field for the group's creation timestamp is `CreationTime` or `Created`, adapt. If `Name` is `GroupName`, adapt.

- [ ] **Step 7: Remove `errors` import if now unused**

After replacing the four stubs (which used `errors.New`), the `errors` import may be unused. Check with `go vet` and remove if so.

- [ ] **Step 8: Build and test**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: implement CreateGroup + GetGroupInfo + UpdateGroupParticipants + LeaveGroup"
```

---

## Task 3: service CreateGroup + tests

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:

```go
type groupFakeWA struct {
	fakeWA

	// CreateGroup capture
	gotCreateName    string
	gotCreateParts   []string
	createResp       waclient.Group
	createErr        error

	// GetGroupInfo capture
	gotInfoJID  string
	infoResp    waclient.Group
	infoErr     error

	// UpdateGroupParticipants capture
	gotUpdateGroupJID string
	gotUpdateAction   string
	gotUpdateParts    []string
	updateResp        []waclient.ParticipantChange
	updateErr         error

	// LeaveGroup capture
	gotLeaveJID string
	leaveErr    error
}

func (f *groupFakeWA) CreateGroup(_ context.Context, name string, participantJIDs []string) (waclient.Group, error) {
	f.gotCreateName = name
	f.gotCreateParts = participantJIDs
	return f.createResp, f.createErr
}
func (f *groupFakeWA) GetGroupInfo(_ context.Context, groupJID string) (waclient.Group, error) {
	f.gotInfoJID = groupJID
	return f.infoResp, f.infoErr
}
func (f *groupFakeWA) UpdateGroupParticipants(_ context.Context, groupJID, action string, participantJIDs []string) ([]waclient.ParticipantChange, error) {
	f.gotUpdateGroupJID = groupJID
	f.gotUpdateAction = action
	f.gotUpdateParts = participantJIDs
	return f.updateResp, f.updateErr
}
func (f *groupFakeWA) LeaveGroup(_ context.Context, groupJID string) error {
	f.gotLeaveJID = groupJID
	return f.leaveErr
}

func TestCreateGroupHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		createResp: waclient.Group{
			JID: "g1@g.us", Name: "Test", OwnerJID: jid,
			CreatedAt: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
			Participants: []waclient.GroupMember{
				{JID: jid, IsAdmin: true, IsSuperAdmin: true},
				{JID: "alice@s.whatsapp.net"},
			},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.CreateGroup(ctx, "Test", []string{"alice@s.whatsapp.net"})
	require.NoError(t, err)
	assert.Equal(t, "g1@g.us", got.JID)
	assert.Equal(t, "Test", wa.gotCreateName)
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, wa.gotCreateParts)

	// Chat row upserted with kind=group.
	require.Contains(t, *chats, "g1@g.us")
	chat := (*chats)["g1@g.us"]
	assert.Equal(t, "group", chat.Kind)
	assert.Equal(t, "Test", chat.Name)
}

func TestCreateGroupValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct {
		label    string
		name     string
		parts    []string
	}{
		{"empty name", "", []string{"alice@s.whatsapp.net"}},
		{"whitespace name", "  ", []string{"alice@s.whatsapp.net"}},
		{"name too long", strings.Repeat("a", 26), []string{"alice@s.whatsapp.net"}},
		{"empty participants", "Test", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.CreateGroup(context.Background(), tc.name, tc.parts)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestCreateGroupNotConnected(t *testing.T) {
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:    fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		createErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.CreateGroup(context.Background(), "Test", []string{"alice@s.whatsapp.net"})
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))

	// No chat row should have been created.
	assert.Empty(t, *chats)
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run TestCreateGroup
```

Expected: FAIL — `(*svc).CreateGroup` undefined.

- [ ] **Step 3: Implement on Service**

Edit `internal/service/service.go`. Extend the `Service` interface:
```go
CreateGroup(ctx context.Context, name string, participantJIDs []string) (waclient.Group, error)
```

Append the method:
```go
const maxGroupNameLen = 25

func (s *svc) CreateGroup(ctx context.Context, name string, participantJIDs []string) (waclient.Group, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return waclient.Group{}, fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if utf8.RuneCountInString(trimmed) > maxGroupNameLen {
		return waclient.Group{}, fmt.Errorf("%w: name exceeds %d runes", ErrInvalidRequest, maxGroupNameLen)
	}
	if len(participantJIDs) == 0 {
		return waclient.Group{}, fmt.Errorf("%w: at least one participant is required", ErrInvalidRequest)
	}

	group, err := s.wa.CreateGroup(ctx, trimmed, participantJIDs)
	if err != nil {
		return waclient.Group{}, err
	}

	if err := s.bundle.Chats.Put(ctx, store.Chat{
		JID:       group.JID,
		Name:      group.Name,
		Kind:      "group",
		LastMsgAt: time.Now(),
	}); err != nil {
		s.logger.Warn("upsert chat on group create failed", "jid", group.JID, "err", err)
	}

	return group, nil
}
```

Add `"unicode/utf8"` to imports if not present.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -run TestCreateGroup -v
```

Expected: PASS — 6 new tests (1 happy, 4 validation, 1 not-connected).

- [ ] **Step 5: Bridge HTTP fakes for the one new method so the build stays green**

Add `CreateGroup` stub to all 11 HTTP fake services (status_test.go, login_qr_test.go, login_phone_test.go, logout_test.go, messages_test.go, chats_test.go, contacts_test.go, stats_test.go, media_test.go, reactions_test.go, receipts_test.go):

For each fake `X`:
```go
func (f X) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
```

Adapt receiver style (value vs pointer) per fake.

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "service: CreateGroup with name validation + chats upsert"
```

---

## Task 4: service ListGroupMembers + UpdateGroupMembers + LeaveGroup

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestListGroupMembersHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		infoResp: waclient.Group{
			JID: "g1@g.us", Name: "Test", OwnerJID: jid,
			Participants: []waclient.GroupMember{
				{JID: jid, IsAdmin: true, IsSuperAdmin: true},
				{JID: "alice@s.whatsapp.net"},
			},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.ListGroupMembers(ctx, "g1@g.us")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "g1@g.us", wa.gotInfoJID)
}

func TestListGroupMembersValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListGroupMembers(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListGroupMembersNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:  fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		infoErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListGroupMembers(context.Background(), "g1@g.us")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestUpdateGroupMembersHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		updateResp: []waclient.ParticipantChange{
			{JID: "alice@s.whatsapp.net", OK: true},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.UpdateGroupMembers(ctx, "g1@g.us", "add", []string{"alice@s.whatsapp.net"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].OK)
	assert.Equal(t, "g1@g.us", wa.gotUpdateGroupJID)
	assert.Equal(t, "add", wa.gotUpdateAction)
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, wa.gotUpdateParts)
}

func TestUpdateGroupMembersValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct {
		label  string
		group  string
		action string
		parts  []string
	}{
		{"empty group jid", "", "add", []string{"alice@s.whatsapp.net"}},
		{"bad action", "g1@g.us", "yelling", []string{"alice@s.whatsapp.net"}},
		{"empty participants", "g1@g.us", "add", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.UpdateGroupMembers(context.Background(), tc.group, tc.action, tc.parts)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestLeaveGroupHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	// Pre-seed a chat row so we can verify it's untouched after leaving.
	(*chats)["g1@g.us"] = store.Chat{JID: "g1@g.us", Kind: "group", Name: "Test"}

	require.NoError(t, s.LeaveGroup(ctx, "g1@g.us"))
	assert.Equal(t, "g1@g.us", wa.gotLeaveJID)

	// History preserved.
	require.Contains(t, *chats, "g1@g.us")
}

func TestLeaveGroupValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.LeaveGroup(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestLeaveGroupNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:   fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		leaveErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.LeaveGroup(context.Background(), "g1@g.us")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/service/... -run 'TestListGroupMembers|TestUpdateGroupMembers|TestLeaveGroup'
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement on Service**

Edit `internal/service/service.go`. Extend the Service interface:
```go
ListGroupMembers(ctx context.Context, groupJID string) ([]waclient.GroupMember, error)
UpdateGroupMembers(ctx context.Context, groupJID, action string, participantJIDs []string) ([]waclient.ParticipantChange, error)
LeaveGroup(ctx context.Context, groupJID string) error
```

Append the methods:
```go
func (s *svc) ListGroupMembers(ctx context.Context, groupJID string) ([]waclient.GroupMember, error) {
	if strings.TrimSpace(groupJID) == "" {
		return nil, fmt.Errorf("%w: group_jid is required", ErrInvalidRequest)
	}
	group, err := s.wa.GetGroupInfo(ctx, groupJID)
	if err != nil {
		return nil, err
	}
	return group.Participants, nil
}

func (s *svc) UpdateGroupMembers(ctx context.Context, groupJID, action string, participantJIDs []string) ([]waclient.ParticipantChange, error) {
	if strings.TrimSpace(groupJID) == "" {
		return nil, fmt.Errorf("%w: group_jid is required", ErrInvalidRequest)
	}
	if action != "add" && action != "remove" {
		return nil, fmt.Errorf("%w: action must be add or remove", ErrInvalidRequest)
	}
	if len(participantJIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one participant is required", ErrInvalidRequest)
	}
	return s.wa.UpdateGroupParticipants(ctx, groupJID, action, participantJIDs)
}

func (s *svc) LeaveGroup(ctx context.Context, groupJID string) error {
	if strings.TrimSpace(groupJID) == "" {
		return fmt.Errorf("%w: group_jid is required", ErrInvalidRequest)
	}
	return s.wa.LeaveGroup(ctx, groupJID)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — 8 new tests + existing.

- [ ] **Step 5: Bridge HTTP fakes for the 3 new methods**

Add to all 11 HTTP fake services:
```go
func (f X) ListGroupMembers(context.Context, string) ([]waclient.GroupMember, error) {
	return nil, nil
}
func (f X) UpdateGroupMembers(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f X) LeaveGroup(context.Context, string) error { return nil }
```

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/
git commit -m "service: ListGroupMembers + UpdateGroupMembers + LeaveGroup"
```

---

## Task 5: HTTP handlers + routes + tests

**Files:**
- Create: `internal/transport/http/groups.go`
- Create: `internal/transport/http/groups_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/groups_test.go`:

```go
package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeGroupsSvc struct {
	createResp waclient.Group
	createErr  error
	infoResp   []waclient.GroupMember
	infoErr    error
	updateResp []waclient.ParticipantChange
	updateErr  error
	leaveErr   error

	gotCreateName  string
	gotCreateParts []string
	gotInfoJID     string
	gotUpdateGroup string
	gotUpdateAction string
	gotUpdateParts []string
	gotLeaveJID    string
}

// minimal Service surface implementations omitted for brevity — copy the
// full set from any existing fake (e.g. fakeReactionsSvc) and override the
// four group methods. Below shows just the four group methods + a stub
// pattern for the rest.
func (f *fakeGroupsSvc) Status(context.Context) (waclient.Status, error)                       { return waclient.Status{}, nil }
func (f *fakeGroupsSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error)              { return nil, nil }
func (f *fakeGroupsSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) { return nil, nil }
func (f *fakeGroupsSvc) Logout(context.Context) error                                          { return nil }
func (f *fakeGroupsSvc) SendText(context.Context, string, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) GetChat(context.Context, string) (store.Chat, error) { return store.Chat{}, nil }
func (f *fakeGroupsSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeGroupsSvc) ListContacts(context.Context) ([]store.Contact, error)               { return nil, nil }
func (f *fakeGroupsSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) { return nil, nil }
func (f *fakeGroupsSvc) Stats(context.Context) (service.Stats, error)                        { return service.Stats{}, nil }
func (f *fakeGroupsSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
func (f *fakeGroupsSvc) EditMessage(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeGroupsSvc) DeleteMessage(context.Context, string) error                  { return nil }
func (f *fakeGroupsSvc) SendReaction(context.Context, string, string) error           { return nil }
func (f *fakeGroupsSvc) ListReactions(context.Context, string) ([]store.Reaction, error) { return nil, nil }
func (f *fakeGroupsSvc) MarkMessageRead(context.Context, string) error                { return nil }
func (f *fakeGroupsSvc) SendTyping(context.Context, string, string) error             { return nil }
func (f *fakeGroupsSvc) ListReceipts(context.Context, string) ([]store.Receipt, error) { return nil, nil }

func (f *fakeGroupsSvc) CreateGroup(_ context.Context, name string, parts []string) (waclient.Group, error) {
	f.gotCreateName = name
	f.gotCreateParts = parts
	return f.createResp, f.createErr
}
func (f *fakeGroupsSvc) ListGroupMembers(_ context.Context, jid string) ([]waclient.GroupMember, error) {
	f.gotInfoJID = jid
	return f.infoResp, f.infoErr
}
func (f *fakeGroupsSvc) UpdateGroupMembers(_ context.Context, jid, action string, parts []string) ([]waclient.ParticipantChange, error) {
	f.gotUpdateGroup = jid
	f.gotUpdateAction = action
	f.gotUpdateParts = parts
	return f.updateResp, f.updateErr
}
func (f *fakeGroupsSvc) LeaveGroup(_ context.Context, jid string) error {
	f.gotLeaveJID = jid
	return f.leaveErr
}

var _ service.Service = (*fakeGroupsSvc)(nil)

func TestCreateGroupHTTPHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := &fakeGroupsSvc{createResp: waclient.Group{
		JID: "g1@g.us", Name: "Test", OwnerJID: "me@s.whatsapp.net",
		CreatedAt: now,
		Participants: []waclient.GroupMember{
			{JID: "me@s.whatsapp.net", IsAdmin: true, IsSuperAdmin: true},
			{JID: "alice@s.whatsapp.net"},
		},
	}}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"name":"Test","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	assert.Equal(t, "Test", f.gotCreateName)
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, f.gotCreateParts)

	var resp struct {
		JID     string             `json:"jid"`
		Name    string             `json:"name"`
		Members []map[string]any   `json:"members"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	assert.Equal(t, "g1@g.us", resp.JID)
	assert.Equal(t, "Test", resp.Name)
	assert.Len(t, resp.Members, 2)
}

func TestCreateGroupHTTPBadJSON(t *testing.T) {
	f := &fakeGroupsSvc{}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCreateGroupHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{createErr: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.CreateGroupHandler(f))
	defer srv.Close()

	body := bytes.NewBufferString(`{"name":"Test","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL, "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestListGroupMembersHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{infoResp: []waclient.GroupMember{
		{JID: "alice@s.whatsapp.net", IsAdmin: true},
	}}
	r := chi.NewRouter()
	r.Get("/v1/groups/{jid}/members", httpapi.ListGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/groups/g1@g.us/members")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotInfoJID)

	var body struct {
		Members []map[string]any `json:"members"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Len(t, body.Members, 1)
	assert.Equal(t, "alice@s.whatsapp.net", body.Members[0]["jid"])
	assert.Equal(t, true, body.Members[0]["is_admin"])
}

func TestListGroupMembersHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{infoErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Get("/v1/groups/{jid}/members", httpapi.ListGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/groups/g1@g.us/members")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestUpdateGroupMembersHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{updateResp: []waclient.ParticipantChange{
		{JID: "alice@s.whatsapp.net", OK: true},
	}}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"action":"add","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotUpdateGroup)
	assert.Equal(t, "add", f.gotUpdateAction)

	var resp struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	require.Len(t, resp.Results, 1)
	assert.Equal(t, true, resp.Results[0]["ok"])
}

func TestUpdateGroupMembersHTTPBadJSON(t *testing.T) {
	f := &fakeGroupsSvc{}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", bytes.NewBufferString("not json"))
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestUpdateGroupMembersHTTPBadAction(t *testing.T) {
	f := &fakeGroupsSvc{updateErr: service.ErrInvalidRequest}
	r := chi.NewRouter()
	r.Post("/v1/groups/{jid}/members", httpapi.UpdateGroupMembersHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := bytes.NewBufferString(`{"action":"yelling","participants":["alice@s.whatsapp.net"]}`)
	res, err := http.Post(srv.URL+"/v1/groups/g1@g.us/members", "application/json", body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLeaveGroupHTTPHappyPath(t *testing.T) {
	f := &fakeGroupsSvc{}
	r := chi.NewRouter()
	r.Delete("/v1/groups/{jid}/membership", httpapi.LeaveGroupHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/groups/g1@g.us/membership", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "g1@g.us", f.gotLeaveJID)
}

func TestLeaveGroupHTTPNotConnected(t *testing.T) {
	f := &fakeGroupsSvc{leaveErr: waclient.ErrNotConnected}
	r := chi.NewRouter()
	r.Delete("/v1/groups/{jid}/membership", httpapi.LeaveGroupHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/groups/g1@g.us/membership", nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}
```

- [ ] **Step 2: Confirm tests fail**

```bash
go test ./internal/transport/http/... -run 'TestCreateGroupHTTP|TestListGroupMembersHTTP|TestUpdateGroupMembersHTTP|TestLeaveGroupHTTP'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/groups.go`:
```go
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

type createGroupRequest struct {
	Name         string   `json:"name"`
	Participants []string `json:"participants"`
}

type updateGroupMembersRequest struct {
	Action       string   `json:"action"`
	Participants []string `json:"participants"`
}

// CreateGroupHandler handles POST /v1/groups.
func CreateGroupHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req createGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		group, err := svc.CreateGroup(r.Context(), req.Name, req.Participants)
		switch {
		case err == nil:
			writeJSON(w, http.StatusCreated, encodeGroup(group))
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}

// ListGroupMembersHandler handles GET /v1/groups/{jid}/members.
func ListGroupMembersHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groupJID := chi.URLParam(r, "jid")
		members, err := svc.ListGroupMembers(r.Context(), groupJID)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{"members": encodeMembers(members)})
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
		}
	})
}

// UpdateGroupMembersHandler handles POST /v1/groups/{jid}/members.
func UpdateGroupMembersHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groupJID := chi.URLParam(r, "jid")
		var req updateGroupMembersRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		results, err := svc.UpdateGroupMembers(r.Context(), groupJID, req.Action, req.Participants)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{"results": encodeChanges(results)})
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}

// LeaveGroupHandler handles DELETE /v1/groups/{jid}/membership.
func LeaveGroupHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groupJID := chi.URLParam(r, "jid")
		err := svc.LeaveGroup(r.Context(), groupJID)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}

func encodeGroup(g waclient.Group) map[string]any {
	return map[string]any{
		"jid":        g.JID,
		"name":       g.Name,
		"owner_jid":  g.OwnerJID,
		"created_at": g.CreatedAt.UTC().Format(time.RFC3339),
		"members":    encodeMembers(g.Participants),
	}
}

func encodeMembers(ms []waclient.GroupMember) []map[string]any {
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]any{
			"jid":             m.JID,
			"is_admin":        m.IsAdmin,
			"is_super_admin":  m.IsSuperAdmin,
		})
	}
	return out
}

func encodeChanges(cs []waclient.ParticipantChange) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		obj := map[string]any{
			"jid": c.JID,
			"ok":  c.OK,
		}
		if c.Error != "" {
			obj["error"] = c.Error
		}
		out = append(out, obj)
	}
	return out
}
```

- [ ] **Step 4: Wire 4 routes**

Edit `internal/transport/http/router.go`. In the auth-protected group, append:
```go
r.Method(http.MethodPost,   "/groups", CreateGroupHandler(d.Service))
r.Method(http.MethodGet,    "/groups/{jid}/members", ListGroupMembersHandler(d.Service))
r.Method(http.MethodPost,   "/groups/{jid}/members", UpdateGroupMembersHandler(d.Service))
r.Method(http.MethodDelete, "/groups/{jid}/membership", LeaveGroupHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -race
```

Expected: PASS — 9 new HTTP tests + existing.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/groups.go internal/transport/http/groups_test.go internal/transport/http/router.go
git commit -m "http: POST /groups + GET/POST /groups/{jid}/members + DELETE /groups/{jid}/membership"
```

---

## Task 6: End-to-end smoke

**Files:** none modified.

- [ ] **Step 1: Build and start daemon**

```bash
pkill -f "whatsmeow-api serve" 2>/dev/null; sleep 1
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
```

Expected: `app store opened`, `server starting`, no errors.

- [ ] **Step 2: Validation paths**

```bash
# 1. Bad JSON on create → 400
curl -i -X POST -H "Content-Type: application/json" -d 'not json' \
  http://127.0.0.1:8080/v1/groups

# 2. Empty name → 400
curl -i -X POST -H "Content-Type: application/json" -d '{"name":"","participants":["alice@s.whatsapp.net"]}' \
  http://127.0.0.1:8080/v1/groups

# 3. Empty participants → 400
curl -i -X POST -H "Content-Type: application/json" -d '{"name":"Test","participants":[]}' \
  http://127.0.0.1:8080/v1/groups

# 4. Valid create, daemon not paired → 409
curl -i -X POST -H "Content-Type: application/json" -d '{"name":"Test","participants":["alice@s.whatsapp.net"]}' \
  http://127.0.0.1:8080/v1/groups

# 5. List members not paired → 409
curl -i http://127.0.0.1:8080/v1/groups/g1@g.us/members

# 6. Bad action on members update → 400
curl -i -X POST -H "Content-Type: application/json" -d '{"action":"yelling","participants":["alice@s.whatsapp.net"]}' \
  http://127.0.0.1:8080/v1/groups/g1@g.us/members

# 7. Leave not paired → 409
curl -i -X DELETE http://127.0.0.1:8080/v1/groups/g1@g.us/membership
```

Expected: 400 / 400 / 400 / 409 / 409 / 400 / 409.

- [ ] **Step 3: (Optional) Real round-trip with paired account**

If you have a paired account:
```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"name":"Plan08 Test","participants":["<RECIPIENT_JID>"]}' \
  http://127.0.0.1:8080/v1/groups
# → 201 with full group info; recipient sees the new group on their phone

GROUP_JID=$(curl -s ... | jq -r .jid)
curl -s "http://127.0.0.1:8080/v1/groups/$GROUP_JID/members"
# → 200 with members array

curl -X POST -d '{"action":"add","participants":["<ANOTHER_JID>"]}' \
  -H "Content-Type: application/json" \
  "http://127.0.0.1:8080/v1/groups/$GROUP_JID/members"
# → 200 with results

curl -X DELETE "http://127.0.0.1:8080/v1/groups/$GROUP_JID/membership"
# → 204
```

- [ ] **Step 4: Stop daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -3 /tmp/wmapi.log
```

Expected: `... msg="server stopped"`.

- [ ] **Step 5: No commit**

---

## Task 7: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update Status section**

Edit `README.md`. Find the Plan 07c entry; append a new line for Plan 08:
```markdown
- **Plan 07c (read receipts + typing)** shipped: ...
- **Plan 08 (groups)** shipped: `POST /v1/groups` creates a group (chat row upserted with `kind=group`); `GET /v1/groups/{jid}/members` lists members live; `POST /v1/groups/{jid}/members` adds or removes members (returns per-JID outcomes); `DELETE /v1/groups/{jid}/membership` leaves the group (history preserved). All four use whatsmeow's group APIs directly — no schema changes.
```

Update the trailing line:
```markdown
SSE event stream lands in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 08"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS
- [ ] Manual smoke (Task 6 Step 2): 400 / 400 / 400 / 409 / 409 / 400 / 409
- [ ] (Optional with paired account) Task 6 Step 3: full round-trip
- [ ] `git log --oneline` shows ~6 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 09 — SSE event stream + Last-Event-ID resume**.

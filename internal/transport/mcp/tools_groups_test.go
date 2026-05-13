package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// --- wa_create_group ---

func TestWACreateGroup_HappyPath(t *testing.T) {
	created := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	svc := &fakeService{
		createGroupFn: func(_ context.Context, name string, jids []string) (waclient.Group, error) {
			require.Equal(t, "Test Group", name)
			require.Equal(t, []string{"1@s.whatsapp.net"}, jids)
			return waclient.Group{
				JID:       "grp1@g.us",
				Name:      "Test Group",
				OwnerJID:  "me@s.whatsapp.net",
				CreatedAt: created,
				Participants: []waclient.GroupMember{
					{JID: "1@s.whatsapp.net", IsAdmin: false},
					{JID: "me@s.whatsapp.net", IsAdmin: true, IsSuperAdmin: true},
				},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_create_group",
		Arguments: map[string]any{
			"name":             "Test Group",
			"participant_jids": []any{"1@s.whatsapp.net"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waCreateGroupOutput](t, res)
	require.Equal(t, "grp1@g.us", out.Group.JID)
	require.Equal(t, "Test Group", out.Group.Name)
	require.Equal(t, "me@s.whatsapp.net", out.Group.OwnerJID)
	require.Len(t, out.Group.Members, 2)
	require.Equal(t, "1@s.whatsapp.net", out.Group.Members[0].JID)
	require.False(t, out.Group.Members[0].IsAdmin)
	require.True(t, out.Group.Members[1].IsAdmin)
	require.True(t, out.Group.Members[1].IsSuperAdmin)
}

func TestWACreateGroup_InvalidRequest(t *testing.T) {
	svc := &fakeService{
		createGroupFn: func(_ context.Context, _ string, _ []string) (waclient.Group, error) {
			return waclient.Group{}, fmt.Errorf("%w: must have at least one participant", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_create_group",
		Arguments: map[string]any{"name": "Empty", "participant_jids": []any{}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

// --- wa_list_group_members ---

func TestWAListGroupMembers_HappyPath(t *testing.T) {
	svc := &fakeService{
		listGroupMembersFn: func(_ context.Context, jid string) ([]waclient.GroupMember, error) {
			require.Equal(t, "grp1@g.us", jid)
			return []waclient.GroupMember{
				{JID: "a@s.whatsapp.net", IsAdmin: true},
				{JID: "b@s.whatsapp.net"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_group_members",
		Arguments: map[string]any{"group_jid": "grp1@g.us"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waListGroupMembersOutput](t, res)
	require.Len(t, out.Members, 2)
	require.Equal(t, "a@s.whatsapp.net", out.Members[0].JID)
	require.True(t, out.Members[0].IsAdmin)
	require.Equal(t, "b@s.whatsapp.net", out.Members[1].JID)
	require.False(t, out.Members[1].IsAdmin)
}

func TestWAListGroupMembers_NotFound(t *testing.T) {
	svc := &fakeService{
		listGroupMembersFn: func(_ context.Context, _ string) ([]waclient.GroupMember, error) {
			return nil, fmt.Errorf("%w: group not found", store.ErrNotFound)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_list_group_members",
		Arguments: map[string]any{"group_jid": "nonexistent@g.us"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

// --- wa_update_group_members ---

func TestWAUpdateGroupMembers_HappyPath(t *testing.T) {
	svc := &fakeService{
		updateGroupMembersFn: func(_ context.Context, jid, action string, participants []string) ([]waclient.ParticipantChange, error) {
			require.Equal(t, "grp1@g.us", jid)
			require.Equal(t, "add", action)
			require.Equal(t, []string{"c@s.whatsapp.net"}, participants)
			return []waclient.ParticipantChange{
				{JID: "c@s.whatsapp.net", OK: true},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_update_group_members",
		Arguments: map[string]any{
			"group_jid":        "grp1@g.us",
			"action":           "add",
			"participant_jids": []any{"c@s.whatsapp.net"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waUpdateGroupMembersOutput](t, res)
	require.Len(t, out.Results, 1)
	require.Equal(t, "c@s.whatsapp.net", out.Results[0].JID)
	require.True(t, out.Results[0].OK)
}

func TestWAUpdateGroupMembers_InvalidAction(t *testing.T) {
	svc := &fakeService{
		updateGroupMembersFn: func(_ context.Context, _, _ string, _ []string) ([]waclient.ParticipantChange, error) {
			return nil, fmt.Errorf("%w: unknown action", service.ErrInvalidRequest)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_update_group_members",
		Arguments: map[string]any{
			"group_jid":        "grp1@g.us",
			"action":           "fly",
			"participant_jids": []any{"c@s.whatsapp.net"},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWAUpdateGroupMembers_PartialFailure(t *testing.T) {
	svc := &fakeService{
		updateGroupMembersFn: func(_ context.Context, _, _ string, _ []string) ([]waclient.ParticipantChange, error) {
			return []waclient.ParticipantChange{
				{JID: "ok@s.whatsapp.net", OK: true},
				{JID: "bad@s.whatsapp.net", OK: false, Error: "not found"},
			}, nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wa_update_group_members",
		Arguments: map[string]any{
			"group_jid":        "grp1@g.us",
			"action":           "add",
			"participant_jids": []any{"ok@s.whatsapp.net", "bad@s.whatsapp.net"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waUpdateGroupMembersOutput](t, res)
	require.Len(t, out.Results, 2)
	require.True(t, out.Results[0].OK)
	require.False(t, out.Results[1].OK)
	require.Equal(t, "not found", out.Results[1].Error)
}

// --- wa_leave_group ---

func TestWALeaveGroup_HappyPath(t *testing.T) {
	svc := &fakeService{
		leaveGroupFn: func(_ context.Context, jid string) error {
			require.Equal(t, "grp1@g.us", jid)
			return nil
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_leave_group",
		Arguments: map[string]any{"group_jid": "grp1@g.us"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	out := decodeStructured[waOK](t, res)
	require.True(t, out.OK)
}

func TestWALeaveGroup_ForbiddenMapsToToolError(t *testing.T) {
	svc := &fakeService{
		leaveGroupFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("%w: not a member", service.ErrForbidden)
		},
	}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "wa_leave_group",
		Arguments: map[string]any{"group_jid": "grp1@g.us"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type waCreateGroupInput struct {
	Name            string   `json:"name" jsonschema:"Display name of the new group."`
	ParticipantJIDs []string `json:"participant_jids" jsonschema:"JIDs of the initial participants (must include at least one besides yourself)."`
}

type waGroupMemberOutput struct {
	JID          string `json:"jid"`
	IsAdmin      bool   `json:"is_admin,omitempty"`
	IsSuperAdmin bool   `json:"is_super_admin,omitempty"`
}

// waGroupOutput mirrors the REST encodeGroup output (key "members", not "participants").
type waGroupOutput struct {
	JID       string                `json:"jid"`
	Name      string                `json:"name"`
	OwnerJID  string                `json:"owner_jid,omitempty"`
	CreatedAt time.Time             `json:"created_at,omitempty"`
	Members   []waGroupMemberOutput `json:"members"`
}

type waCreateGroupOutput struct {
	Group waGroupOutput `json:"group"`
}

type waGroupJIDInput struct {
	GroupJID string `json:"group_jid" jsonschema:"WhatsApp JID of the group (e.g. 123456789@g.us)."`
}

type waListGroupMembersOutput struct {
	Members []waGroupMemberOutput `json:"members"`
}

type waUpdateGroupMembersInput struct {
	GroupJID        string   `json:"group_jid" jsonschema:"WhatsApp JID of the group."`
	Action          string   `json:"action" jsonschema:"One of: add, remove, promote, demote."`
	ParticipantJIDs []string `json:"participant_jids" jsonschema:"JIDs of the participants to act on."`
}

type waParticipantChangeOutput struct {
	JID   string `json:"jid"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// waUpdateGroupMembersOutput mirrors the REST key "results".
type waUpdateGroupMembersOutput struct {
	Results []waParticipantChangeOutput `json:"results"`
}

func registerGroupTools(srv *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_create_group",
		Description: "Create a new WhatsApp group with the given name and initial participants (must include at least one besides yourself).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waCreateGroupInput) (*mcpsdk.CallToolResult, waCreateGroupOutput, error) {
		g, err := d.Service.CreateGroup(ctx, in.Name, in.ParticipantJIDs)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waCreateGroupOutput{}, terr
		}
		return nil, waCreateGroupOutput{Group: groupToOutput(g)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_list_group_members",
		Description: "List members of a group. Read-only.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGroupJIDInput) (*mcpsdk.CallToolResult, waListGroupMembersOutput, error) {
		rows, err := d.Service.ListGroupMembers(ctx, in.GroupJID)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waListGroupMembersOutput{}, terr
		}
		return nil, waListGroupMembersOutput{Members: groupMembersToOutput(rows)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_update_group_members",
		Description: "Add, remove, promote, or demote group participants. Returns a per-participant change result.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waUpdateGroupMembersInput) (*mcpsdk.CallToolResult, waUpdateGroupMembersOutput, error) {
		rows, err := d.Service.UpdateGroupMembers(ctx, in.GroupJID, in.Action, in.ParticipantJIDs)
		if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
			return res, waUpdateGroupMembersOutput{}, terr
		}
		return nil, waUpdateGroupMembersOutput{Results: participantChangesToOutput(rows)}, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "wa_leave_group",
		Description: "Leave a group. The daemon will no longer receive messages from this group.",
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in waGroupJIDInput) (*mcpsdk.CallToolResult, waOK, error) {
		if err := d.Service.LeaveGroup(ctx, in.GroupJID); err != nil {
			if res, terr := mapErr(err, d.Logger); terr != nil || res != nil {
				return res, waOK{}, terr
			}
		}
		return nil, waOK{OK: true}, nil
	})
}

func groupToOutput(g waclient.Group) waGroupOutput {
	return waGroupOutput{
		JID:       g.JID,
		Name:      g.Name,
		OwnerJID:  g.OwnerJID,
		CreatedAt: g.CreatedAt,
		Members:   groupMembersToOutput(g.Participants),
	}
}

func groupMembersToOutput(rows []waclient.GroupMember) []waGroupMemberOutput {
	out := make([]waGroupMemberOutput, 0, len(rows))
	for _, m := range rows {
		out = append(out, waGroupMemberOutput{JID: m.JID, IsAdmin: m.IsAdmin, IsSuperAdmin: m.IsSuperAdmin})
	}
	return out
}

func participantChangesToOutput(rows []waclient.ParticipantChange) []waParticipantChangeOutput {
	out := make([]waParticipantChangeOutput, 0, len(rows))
	for _, c := range rows {
		out = append(out, waParticipantChangeOutput{JID: c.JID, OK: c.OK, Error: c.Error})
	}
	return out
}

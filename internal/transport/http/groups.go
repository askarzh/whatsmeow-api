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
			"jid":            m.JID,
			"is_admin":       m.IsAdmin,
			"is_super_admin": m.IsSuperAdmin,
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

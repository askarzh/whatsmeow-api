package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

type sendReactionRequest struct {
	Emoji string `json:"emoji"`
}

// SendReactionHandler handles POST /v1/messages/{id}/reactions.
// Body: {"emoji": "..."}. Empty emoji clears the daemon's reaction.
func SendReactionHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		var req sendReactionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}

		err := svc.SendReaction(r.Context(), messageID, req.Emoji)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "message.not_found", err.Error())
		case errors.Is(err, waclient.ErrNotConnected):
			WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
		}
	})
}

// ListReactionsHandler handles GET /v1/messages/{id}/reactions.
// 200 with {"reactions": [{message_id, sender_jid, emoji, ts}, ...]}.
func ListReactionsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		reactions, err := svc.ListReactions(r.Context(), messageID)
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"reactions": encodeReactions(reactions),
		})
	})
}

func encodeReaction(r store.Reaction) map[string]any {
	return map[string]any{
		"message_id": r.MessageID,
		"sender_jid": r.SenderJID,
		"emoji":      r.Emoji,
		"ts":         r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func encodeReactions(rs []store.Reaction) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, encodeReaction(r))
	}
	return out
}

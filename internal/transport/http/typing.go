package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type sendTypingRequest struct {
	State string `json:"state"`
}

// SendTypingHandler handles POST /v1/chats/{jid}/typing.
// Body: {"state": "composing"|"paused"}.
// 204 on success, 400 on bad JSON or invalid state, 409 if not connected.
func SendTypingHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatJID := chi.URLParam(r, "jid")
		var req sendTypingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}

		err := svc.SendTyping(r.Context(), chatJID, req.State)
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

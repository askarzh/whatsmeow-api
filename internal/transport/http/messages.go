package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type sendTextRequest struct {
	ChatJID string `json:"chat_jid"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
}

const maxTextLen = 4096

// SendTextHandler handles POST /v1/messages: send a text message to a chat.
func SendTextHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sendTextRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		if req.ChatJID == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "chat_jid is required")
			return
		}
		if req.Text == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "text is required")
			return
		}
		if len(req.Text) > maxTextLen {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "text exceeds 4096 bytes")
			return
		}

		msg, err := svc.SendText(r.Context(), req.ChatJID, req.Text, req.ReplyTo)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			case errors.Is(err, waclient.ErrNotConnected):
				WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       msg.ID,
			"chat_jid": msg.ChatJID,
			"ts":       msg.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	})
}

// SearchMessagesHandler handles GET /v1/messages/search?q=...&limit=...
func SearchMessagesHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "q is required")
			return
		}
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		msgs, err := svc.SearchMessages(r.Context(), q, limit)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": encodeMessages(msgs)})
	})
}

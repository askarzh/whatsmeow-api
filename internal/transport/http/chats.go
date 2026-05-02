package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/go-chi/chi/v5"
)

const (
	defaultLimit = 50
	maxAPILimit  = 100
)

// parseLimit reads ?limit=N (default 50, range [1, 100]).
func parseLimit(r *http.Request) (int, error) {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 || n > maxAPILimit {
		return 0, errors.New("limit must be in [1, 100]")
	}
	return n, nil
}

// parseBefore reads ?before=<RFC3339>; absent or empty → zero time (no cursor).
func parseBefore(r *http.Request) (time.Time, error) {
	q := r.URL.Query().Get("before")
	if q == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, q)
	if err != nil {
		return time.Time{}, errors.New("before must be RFC 3339 timestamp")
	}
	return t, nil
}

// parseIncludeArchived reads ?include_archived=<bool>; default false.
func parseIncludeArchived(r *http.Request) (bool, error) {
	q := r.URL.Query().Get("include_archived")
	if q == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(q)
	if err != nil {
		return false, errors.New("include_archived must be true or false")
	}
	return b, nil
}

// ListChatsHandler handles GET /v1/chats.
func ListChatsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		before, err := parseBefore(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		inclArch, err := parseIncludeArchived(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		chats, err := svc.ListChats(r.Context(), before, limit, inclArch)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chats": encodeChats(chats)})
	})
}

// GetChatHandler handles GET /v1/chats/{jid}.
func GetChatHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jid := chi.URLParam(r, "jid")
		c, err := svc.GetChat(r.Context(), jid)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, encodeChat(c))
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "chat.not_found", "no chat with that jid")
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
		}
	})
}

// ListMessagesByChatHandler handles GET /v1/chats/{jid}/messages.
func ListMessagesByChatHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jid := chi.URLParam(r, "jid")
		limit, err := parseLimit(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}
		before, err := parseBefore(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		msgs, err := svc.ListMessages(r.Context(), jid, before, limit)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": encodeMessages(msgs)})
	})
}

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func encodeChat(c store.Chat) map[string]any {
	return map[string]any{
		"jid":          c.JID,
		"name":         nilOrString(strPtr(c.Name)),
		"kind":         c.Kind,
		"last_msg_at":  nilOrTime(timePtr(c.LastMsgAt)),
		"unread_count": c.UnreadCount,
		"archived":     c.Archived,
	}
}

func encodeChats(chats []store.Chat) []map[string]any {
	out := make([]map[string]any, 0, len(chats))
	for _, c := range chats {
		out = append(out, encodeChat(c))
	}
	return out
}

func encodeMessage(m store.Message) map[string]any {
	return map[string]any{
		"id":         m.ID,
		"chat_jid":   m.ChatJID,
		"sender_jid": m.SenderJID,
		"ts":         m.Timestamp.UTC().Format(time.RFC3339),
		"kind":       m.Kind,
		"body":       m.Body,
		"reply_to":   nilOrString(strPtr(m.ReplyTo)),
		"edited_at":  nilOrTime(m.EditedAt),
		"deleted_at": nilOrTime(m.DeletedAt),
	}
}

func encodeMessages(msgs []store.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, encodeMessage(m))
	}
	return out
}

// strPtr returns nil if s is empty, else &s — keeps "" out of the JSON shape.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timePtr returns nil if t is zero, else &t — keeps the zero time out of JSON.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

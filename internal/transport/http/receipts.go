package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// MarkReadHandler handles POST /v1/messages/{id}/read.
// Sends a read receipt to the WhatsApp peer and decrements the unread count.
// 204 on success, 404 if the message doesn't exist, 409 if not connected.
func MarkReadHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		err := svc.MarkMessageRead(r.Context(), messageID)
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

// ListReceiptsHandler handles GET /v1/messages/{id}/receipts.
// Returns {"receipts": [{message_id, reader_jid, type, ts}, ...]}.
func ListReceiptsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "id")
		receipts, err := svc.ListReceipts(r.Context(), messageID)
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
			"receipts": encodeReceipts(receipts),
		})
	})
}

func encodeReceipt(r store.Receipt) map[string]any {
	return map[string]any{
		"message_id": r.MessageID,
		"reader_jid": r.ReaderJID,
		"type":       r.Type,
		"ts":         r.Timestamp.UTC().Format(time.RFC3339),
	}
}

func encodeReceipts(rs []store.Receipt) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, encodeReceipt(r))
	}
	return out
}

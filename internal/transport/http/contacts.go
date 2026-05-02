package http

import (
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

// ListContactsHandler handles GET /v1/contacts (no pagination — returns all).
func ListContactsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacts, err := svc.ListContacts(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contacts": encodeContacts(contacts)})
	})
}

// SearchContactsHandler handles GET /v1/contacts/search?q=...&limit=...
func SearchContactsHandler(svc service.Service) http.Handler {
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
		contacts, err := svc.SearchContacts(r.Context(), q, limit)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contacts": encodeContacts(contacts)})
	})
}

func encodeContact(c store.Contact) map[string]any {
	return map[string]any{
		"jid":           c.JID,
		"push_name":     nilOrString(strPtr(c.PushName)),
		"full_name":     nilOrString(strPtr(c.FullName)),
		"business_name": nilOrString(strPtr(c.BusinessName)),
	}
}

func encodeContacts(cs []store.Contact) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, encodeContact(c))
	}
	return out
}

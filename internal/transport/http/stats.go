package http

import (
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// StatsHandler handles GET /v1/stats.
func StatsHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := svc.Stats(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
}

package http

import (
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// LogoutHandler tells WhatsApp to invalidate the current session and disconnects.
func LogoutHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := svc.Logout(r.Context())
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, waclient.ErrNotLoggedIn):
			WriteProblem(w, http.StatusConflict, "wa.not_logged_in", err.Error())
		default:
			WriteProblem(w, http.StatusInternalServerError, "wa.internal", err.Error())
		}
	})
}

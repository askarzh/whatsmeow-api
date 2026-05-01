package http

import (
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// LoginQRHandler streams whatsmeow QR codes as SSE events.
func LoginQRHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch, err := svc.LoginQR(r.Context())
		if err != nil {
			switch {
			case errors.Is(err, waclient.ErrAlreadyLoggedIn):
				WriteProblem(w, http.StatusConflict, "wa.already_logged_in", err.Error())
			case errors.Is(err, waclient.ErrLoginInProgress):
				WriteProblem(w, http.StatusConflict, "wa.login_in_progress", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.login_failed", err.Error())
			}
			return
		}

		SSEPrepare(w)
		w.WriteHeader(http.StatusOK)

		for evt := range ch {
			if evt.Terminal {
				_ = SSEWriteEvent(w, "connection", map[string]any{"outcome": evt.Outcome})
				return
			}
			_ = SSEWriteEvent(w, "qr", map[string]any{"code": evt.Code, "expires_in_s": 20})
		}
	})
}

package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

type loginPhoneRequest struct {
	PhoneNumber string `json:"phone_number"`
}

// LoginPhoneHandler streams the phone-pair flow as SSE: first event is the
// pairing code, the terminal event is the connection outcome.
func LoginPhoneHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req loginPhoneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "malformed JSON body")
			return
		}
		if !waclient.IsValidPhoneNumber(req.PhoneNumber) {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "phone_number must be E.164 (e.g. +27821234567)")
			return
		}

		ch, err := svc.LoginPhone(r.Context(), req.PhoneNumber)
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
			_ = SSEWriteEvent(w, "pair_code", map[string]any{"code": evt.Code, "expires_in_s": 120})
		}
	})
}

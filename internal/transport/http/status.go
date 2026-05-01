package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// StatusHandler reports the WhatsApp connection state from the service layer.
func StatusHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.Status(r.Context())
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "wa.internal", err.Error())
			return
		}
		body := map[string]any{
			"wa_connected": st.Connected,
			"jid":          nilOrString(st.JID),
			"push_name":    nilOrString(st.PushName),
			"since":        nilOrTime(st.Since),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}

func nilOrString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nilOrTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC().Format(time.RFC3339)
}

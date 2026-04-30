package http

import (
	"encoding/json"
	"net/http"
)

// StatusHandler returns the WhatsApp connection state. Until Plan 02
// wires the waclient, it is a placeholder reporting "not connected".
func StatusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"wa_connected": false,
			"jid":          nil,
			"since":        nil,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}

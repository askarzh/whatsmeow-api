package http

import (
	"encoding/json"
	"net/http"
)

// HealthHandler returns liveness. db / wa_connected are nil until the
// later plans wire real probes.
func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"ok":           true,
			"db":           nil,
			"wa_connected": nil,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	})
}

package http

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearerToken returns middleware that checks an Authorization: Bearer
// header. When token is empty the middleware is a no-op (auth disabled).
func RequireBearerToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "missing bearer token")
				return
			}
			got := h[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

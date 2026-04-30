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
			got, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "missing bearer token")
				return
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				WriteProblem(w, http.StatusUnauthorized, "auth.unauthorized", "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken extracts the token value from an Authorization header. Returns
// false if the header doesn't start with a case-insensitive "Bearer " scheme
// or the token portion is empty. RFC 6750 §2.1.
func bearerToken(h string) (string, bool) {
	const prefix = "bearer "
	if len(h) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimLeft(h[len(prefix):], " ")
	if tok == "" {
		return "", false
	}
	return tok, true
}

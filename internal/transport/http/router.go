package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps is the bundle of values the router depends on. Plan 02+ will
// extend this with WAClient, Store, etc.
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	Service service.Service
}

func NewRouter(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(slogRequests(d.Logger))
	r.Use(middleware.Recoverer)

	r.Route("/v1", func(r chi.Router) {
		// public
		r.Method(http.MethodGet, "/health", HealthHandler())

		// protected
		r.Group(func(r chi.Router) {
			r.Use(RequireBearerToken(d.Config.Auth.Token))
			r.Method(http.MethodGet, "/status", StatusHandler(d.Service))
			r.Method(http.MethodPost, "/login/qr", LoginQRHandler(d.Service))
			r.Method(http.MethodPost, "/login/phone", LoginPhoneHandler(d.Service))
			r.Method(http.MethodPost, "/logout", LogoutHandler(d.Service))
		})
	})

	return r
}

func slogRequests(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

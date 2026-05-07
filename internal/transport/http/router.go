package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/config"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps is the bundle of values the router depends on.
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	Service service.Service
	Store   store.Bundle
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
			r.Method(http.MethodPost, "/messages", SendTextHandler(d.Service))
			r.Method(http.MethodPatch, "/messages/{id}", EditMessageHandler(d.Service))
			r.Method(http.MethodDelete, "/messages/{id}", DeleteMessageHandler(d.Service))
			r.Method(http.MethodGet, "/chats", ListChatsHandler(d.Service))
			r.Method(http.MethodGet, "/chats/{jid}", GetChatHandler(d.Service))
			r.Method(http.MethodGet, "/chats/{jid}/messages", ListMessagesByChatHandler(d.Service))
			r.Method(http.MethodGet, "/messages/search", SearchMessagesHandler(d.Service))
			r.Method(http.MethodGet, "/contacts", ListContactsHandler(d.Service))
			r.Method(http.MethodGet, "/contacts/search", SearchContactsHandler(d.Service))
			r.Method(http.MethodGet, "/stats", StatsHandler(d.Service))
			r.Method(http.MethodPost, "/media", SendMediaHandler(d.Service, d.Config.HTTP.MaxBodyBytes))
			r.Method(http.MethodGet, "/media/{message_id}", GetMediaHandler(d.Service))
			r.Method(http.MethodPost, "/messages/{id}/reactions", SendReactionHandler(d.Service))
			r.Method(http.MethodGet, "/messages/{id}/reactions", ListReactionsHandler(d.Service))
			r.Method(http.MethodPost, "/messages/{id}/read", MarkReadHandler(d.Service))
			r.Method(http.MethodGet, "/messages/{id}/receipts", ListReceiptsHandler(d.Service))
			r.Method(http.MethodPost, "/chats/{jid}/typing", SendTypingHandler(d.Service))
			r.Method(http.MethodPost, "/groups", CreateGroupHandler(d.Service))
			r.Method(http.MethodGet, "/groups/{jid}/members", ListGroupMembersHandler(d.Service))
			r.Method(http.MethodPost, "/groups/{jid}/members", UpdateGroupMembersHandler(d.Service))
			r.Method(http.MethodDelete, "/groups/{jid}/membership", LeaveGroupHandler(d.Service))
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

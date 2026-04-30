package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type Server struct {
	deps Deps
	addr atomic.Pointer[string]
}

func NewServer(d Deps) *Server { return &Server{deps: d} }

// Addr returns the actual listen address (useful when port=0). Empty until Run binds.
func (s *Server) Addr() string {
	if a := s.addr.Load(); a != nil {
		return *a
	}
	return ""
}

// Run starts the HTTP server. It returns when ctx is cancelled, after a
// graceful 10s shutdown.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.deps.Config.Server.Bind, s.deps.Config.Server.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	resolved := ln.Addr().String()
	s.addr.Store(&resolved)

	server := &http.Server{
		Handler:           NewRouter(s.deps),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}

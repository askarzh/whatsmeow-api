// Package service holds the daemon's use cases. Plan 02 shipped pass-through
// methods over WAClient; Plan 04 adds SendText + inbound persistence over the
// app store.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error
}

type svc struct {
	wa     waclient.WAClient
	bundle store.Bundle
	logger *slog.Logger
}

// New constructs a Service backed by the given WAClient and store bundle.
func New(wa waclient.WAClient, bundle store.Bundle, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &svc{wa: wa, bundle: bundle, logger: logger}
}

func (s *svc) Status(_ context.Context) (waclient.Status, error) {
	return s.wa.Status(), nil
}

func (s *svc) LoginQR(ctx context.Context) (<-chan waclient.QREvent, error) {
	return s.wa.LoginQR(ctx)
}

func (s *svc) LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error) {
	if !waclient.IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}
	return s.wa.LoginPhone(ctx, phoneNumber)
}

func (s *svc) Logout(ctx context.Context) error {
	return s.wa.Logout(ctx)
}

// Package service holds the daemon's use cases. Plan 02 shipped pass-through
// methods over WAClient; Plan 04 adds SendText + inbound persistence over the
// app store.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// ErrInvalidRequest is returned when the caller provides invalid input.
var ErrInvalidRequest = errors.New("service: invalid request")

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error

	SendText(ctx context.Context, chatJID, text string) (store.Message, error)
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

const maxTextLen = 4096

func (s *svc) SendText(ctx context.Context, chatJID, text string) (store.Message, error) {
	if strings.TrimSpace(chatJID) == "" {
		return store.Message{}, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if text == "" {
		return store.Message{}, fmt.Errorf("%w: text is required", ErrInvalidRequest)
	}
	if len(text) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	sent, err := s.wa.SendText(ctx, chatJID, text)
	if err != nil {
		return store.Message{}, err
	}

	msg := store.Message{
		ID:        sent.ID,
		ChatJID:   chatJID,
		SenderJID: sent.SenderJID,
		Timestamp: sent.Timestamp,
		Kind:      "text",
		Body:      text,
	}
	if err := s.bundle.Messages.Put(ctx, msg); err != nil {
		s.logger.Warn("persist outbound message failed; whatsmeow echo will heal", "id", sent.ID, "err", err)
	}
	if err := s.bundle.Chats.Put(ctx, store.Chat{
		JID:       chatJID,
		Kind:      waclient.ChatKindFromJID(chatJID),
		LastMsgAt: sent.Timestamp,
	}); err != nil {
		s.logger.Warn("upsert chat on send failed", "chat_jid", chatJID, "err", err)
	}
	return msg, nil
}

// Imports used by handleIncoming (Task 6); silence "unused" until Task 6.
var _ = time.Time{}

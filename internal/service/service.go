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

// Stats holds aggregate counts for the local store.
type Stats struct {
	Chats       int `json:"chats"`
	Messages    int `json:"messages"`
	Contacts    int `json:"contacts"`
	UnreadTotal int `json:"unread_total"`
}

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error

	SendText(ctx context.Context, chatJID, text string) (store.Message, error)

	// Plan 05
	ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error)
	GetChat(ctx context.Context, jid string) (store.Chat, error)
	ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error)
	ListContacts(ctx context.Context) ([]store.Contact, error)
	SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error)
	Stats(ctx context.Context) (Stats, error)
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
	s := &svc{wa: wa, bundle: bundle, logger: logger}
	wa.OnIncomingMessage(s.handleIncoming)
	return s
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

const (
	maxTextLen = 4096
	minLimit   = 1
	maxLimit   = 100
)

func validateLimit(limit int) error {
	if limit < minLimit || limit > maxLimit {
		return fmt.Errorf("%w: limit must be in [%d, %d]", ErrInvalidRequest, minLimit, maxLimit)
	}
	return nil
}

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
	existing, err := s.bundle.Chats.Get(ctx, chatJID)
	if err != nil {
		existing = store.Chat{JID: chatJID, Kind: waclient.ChatKindFromJID(chatJID)}
	}
	existing.LastMsgAt = sent.Timestamp
	if existing.Kind == "" {
		existing.Kind = waclient.ChatKindFromJID(chatJID)
	}
	if err := s.bundle.Chats.Put(ctx, existing); err != nil {
		s.logger.Warn("upsert chat on send failed", "chat_jid", chatJID, "err", err)
	}
	return msg, nil
}

func (s *svc) ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Chats.List(ctx, beforeMsgAt, limit, includeArchived)
}

func (s *svc) GetChat(ctx context.Context, jid string) (store.Chat, error) {
	if strings.TrimSpace(jid) == "" {
		return store.Chat{}, fmt.Errorf("%w: jid is required", ErrInvalidRequest)
	}
	return s.bundle.Chats.Get(ctx, jid)
}

func (s *svc) ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error) {
	if strings.TrimSpace(chatJID) == "" {
		return nil, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Messages.ListByChat(ctx, chatJID, limit, beforeTS)
}

func (s *svc) SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: q is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Messages.Search(ctx, query, limit)
}

func (s *svc) ListContacts(ctx context.Context) ([]store.Contact, error) {
	return s.bundle.Contacts.List(ctx)
}

func (s *svc) SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: q is required", ErrInvalidRequest)
	}
	if err := validateLimit(limit); err != nil {
		return nil, err
	}
	return s.bundle.Contacts.Search(ctx, query, limit)
}

func (s *svc) Stats(ctx context.Context) (Stats, error) {
	chatsCount, err := s.bundle.Chats.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats chats: %w", err)
	}
	msgsCount, err := s.bundle.Messages.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats messages: %w", err)
	}
	contactsCount, err := s.bundle.Contacts.Count(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats contacts: %w", err)
	}
	unread, err := s.bundle.Chats.TotalUnread(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("stats unread: %w", err)
	}
	return Stats{
		Chats:       chatsCount,
		Messages:    msgsCount,
		Contacts:    contactsCount,
		UnreadTotal: unread,
	}, nil
}

func (s *svc) handleIncoming(msg waclient.IncomingMessage) {
	ctx := context.Background()

	if msg.PushName != "" {
		if err := s.bundle.Contacts.Put(ctx, store.Contact{
			JID:      msg.SenderJID,
			PushName: msg.PushName,
		}); err != nil {
			s.logger.Warn("upsert contact on incoming failed", "jid", msg.SenderJID, "err", err)
		}
	}

	chat, err := s.bundle.Chats.Get(ctx, msg.ChatJID)
	if err != nil {
		// Treat any error (including ErrNotFound) as "no existing chat".
		chat = store.Chat{JID: msg.ChatJID, Kind: msg.ChatKind}
	}
	chat.LastMsgAt = msg.Timestamp
	chat.UnreadCount++
	if chat.Kind == "" {
		chat.Kind = msg.ChatKind
	}
	if err := s.bundle.Chats.Put(ctx, chat); err != nil {
		s.logger.Warn("upsert chat on incoming failed", "jid", msg.ChatJID, "err", err)
	}

	if err := s.bundle.Messages.Put(ctx, store.Message{
		ID:        msg.ID,
		ChatJID:   msg.ChatJID,
		SenderJID: msg.SenderJID,
		Timestamp: msg.Timestamp,
		Kind:      msg.Kind,
		Body:      msg.Body,
	}); err != nil {
		s.logger.Warn("persist incoming message failed", "id", msg.ID, "err", err)
	}
}

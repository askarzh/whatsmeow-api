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

	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// ErrInvalidRequest is returned when the caller provides invalid input.
var ErrInvalidRequest = errors.New("service: invalid request")

// ErrForbidden is returned when the caller is not permitted to perform the
// requested operation (e.g. editing a message they did not send).
var ErrForbidden = errors.New("service: forbidden")

// Stats holds aggregate counts for the local store.
type Stats struct {
	Chats       int `json:"chats"`
	Messages    int `json:"messages"`
	Contacts    int `json:"contacts"`
	UnreadTotal int `json:"unread_total"`
}

// SendMediaRequest is the input for Service.SendMedia.
type SendMediaRequest struct {
	ChatJID  string
	Kind     string // "image" | "document"
	Caption  string // optional, max 4096 bytes
	Filename string // required for document; informational for image
	MIME     string
	Body     []byte
}

// Service is the use-case layer the HTTP handlers depend on.
type Service interface {
	Status(ctx context.Context) (waclient.Status, error)
	LoginQR(ctx context.Context) (<-chan waclient.QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan waclient.PairEvent, error)
	Logout(ctx context.Context) error

	SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error)

	// Plan 05
	ListChats(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error)
	GetChat(ctx context.Context, jid string) (store.Chat, error)
	ListMessages(ctx context.Context, chatJID string, beforeTS time.Time, limit int) ([]store.Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]store.Message, error)
	ListContacts(ctx context.Context) ([]store.Contact, error)
	SearchContacts(ctx context.Context, query string, limit int) ([]store.Contact, error)
	Stats(ctx context.Context) (Stats, error)

	// Plan 06
	SendMedia(ctx context.Context, req SendMediaRequest) (store.Message, error)
	GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error)

	// Plan 07a
	EditMessage(ctx context.Context, messageID, newText string) (store.Message, error)
	DeleteMessage(ctx context.Context, messageID string) error

	// Plan 07b
	SendReaction(ctx context.Context, messageID, emoji string) error
	ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error)

	// Plan 07c
	MarkMessageRead(ctx context.Context, messageID string) error
	SendTyping(ctx context.Context, chatJID, state string) error
	ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error)
}

type svc struct {
	wa         waclient.WAClient
	bundle     store.Bundle
	mediaStore *mediastore.Store
	logger     *slog.Logger
}

// New constructs a Service backed by the given WAClient and store bundle.
func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &svc{wa: wa, bundle: bundle, mediaStore: mediaStore, logger: logger}
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

func (s *svc) SendText(ctx context.Context, chatJID, text, replyTo string) (store.Message, error) {
	if strings.TrimSpace(chatJID) == "" {
		return store.Message{}, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if text == "" {
		return store.Message{}, fmt.Errorf("%w: text is required", ErrInvalidRequest)
	}
	if len(text) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	sent, err := s.wa.SendText(ctx, chatJID, text, replyTo)
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
		ReplyTo:   replyTo, // Plan 07a
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

	// Plan 07b: route reactions BEFORE revoke/edit/normal paths.
	if msg.ReactionTargetID != "" {
		if msg.ReactionEmoji == "" {
			if err := s.bundle.Reactions.Delete(ctx, msg.ReactionTargetID, msg.SenderJID); err != nil {
				s.logger.Warn("clear reaction on incoming failed", "target", msg.ReactionTargetID, "err", err)
			}
		} else {
			if err := s.bundle.Reactions.Put(ctx, store.Reaction{
				MessageID: msg.ReactionTargetID,
				SenderJID: msg.SenderJID,
				Emoji:     msg.ReactionEmoji,
				Timestamp: msg.Timestamp,
			}); err != nil {
				s.logger.Warn("persist reaction on incoming failed", "target", msg.ReactionTargetID, "err", err)
			}
		}
		return
	}

	// Plan 07a: route edits and revokes BEFORE the normal-message path.
	if msg.RevokeOfID != "" {
		if err := s.bundle.Messages.SoftDelete(ctx, msg.RevokeOfID, msg.Timestamp); err != nil {
			s.logger.Warn("soft-delete on incoming revoke failed", "id", msg.RevokeOfID, "err", err)
		}
		return
	}
	if msg.EditOfID != "" {
		existing, err := s.bundle.Messages.Get(ctx, msg.EditOfID)
		if err != nil {
			s.logger.Warn("incoming edit references unknown message", "id", msg.EditOfID, "err", err)
			return
		}
		existing.Body = msg.Body
		t := msg.Timestamp
		existing.EditedAt = &t
		if err := s.bundle.Messages.Put(ctx, existing); err != nil {
			s.logger.Warn("persist incoming edit failed", "id", msg.EditOfID, "err", err)
		}
		return
	}

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

	if msg.MediaDownloader != nil {
		go s.downloadAndPersistMedia(msg.ID, msg.MediaDownloader)
	}
}

// downloadAndPersistMedia runs in a per-message goroutine: it calls the
// downloader closure, writes the bytes to disk via the mediastore, and
// persists the resulting media row. All failures are logged via slog and
// never propagated.
func (s *svc) downloadAndPersistMedia(messageID string, downloader func(context.Context) ([]byte, string, error)) {
	ctx := context.Background()
	body, mime, err := downloader(ctx)
	if err != nil {
		s.logger.Warn("download media failed", "id", messageID, "err", err)
		return
	}
	sha, path, err := s.mediaStore.Write(ctx, body, mime)
	if err != nil {
		s.logger.Warn("write media to disk failed", "id", messageID, "err", err)
		return
	}
	if err := s.bundle.Media.Put(ctx, store.MediaRef{
		MessageID: messageID,
		MIME:      mime,
		Size:      int64(len(body)),
		SHA256:    sha,
		Path:      path,
	}); err != nil {
		s.logger.Warn("persist incoming media row failed", "id", messageID, "err", err)
	}
}

// SendMedia uploads media bytes via whatsmeow, persists the outbound message
// and a content-addressed file under the media store.
func (s *svc) SendMedia(ctx context.Context, req SendMediaRequest) (store.Message, error) {
	if strings.TrimSpace(req.ChatJID) == "" {
		return store.Message{}, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if len(req.Body) == 0 {
		return store.Message{}, fmt.Errorf("%w: body is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.MIME) == "" {
		return store.Message{}, fmt.Errorf("%w: mime is required", ErrInvalidRequest)
	}
	if req.Kind != "image" && req.Kind != "document" {
		return store.Message{}, fmt.Errorf("%w: kind must be image or document", ErrInvalidRequest)
	}
	if req.Kind == "document" && strings.TrimSpace(req.Filename) == "" {
		return store.Message{}, fmt.Errorf("%w: filename is required for documents", ErrInvalidRequest)
	}
	if len(req.Caption) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: caption exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	sent, err := s.wa.SendMedia(ctx, req.ChatJID, req.Kind, req.Caption, req.Filename, req.MIME, req.Body)
	if err != nil {
		return store.Message{}, err
	}

	sha, path, werr := s.mediaStore.Write(ctx, req.Body, req.MIME)
	if werr != nil {
		s.logger.Warn("write media to disk failed", "id", sent.ID, "err", werr)
		sha = ""
		path = ""
	}

	msg := store.Message{
		ID:        sent.ID,
		ChatJID:   req.ChatJID,
		SenderJID: sent.SenderJID,
		Timestamp: sent.Timestamp,
		Kind:      req.Kind,
		Body:      req.Caption,
	}
	if err := s.bundle.Messages.Put(ctx, msg); err != nil {
		s.logger.Warn("persist outbound media message failed", "id", sent.ID, "err", err)
	}

	if path != "" {
		if err := s.bundle.Media.Put(ctx, store.MediaRef{
			MessageID: sent.ID,
			MIME:      req.MIME,
			Size:      int64(len(req.Body)),
			SHA256:    sha,
			Path:      path,
		}); err != nil {
			s.logger.Warn("persist media row failed", "id", sent.ID, "err", err)
		}
	}

	existing, err := s.bundle.Chats.Get(ctx, req.ChatJID)
	if err != nil {
		existing = store.Chat{JID: req.ChatJID, Kind: waclient.ChatKindFromJID(req.ChatJID)}
	}
	existing.LastMsgAt = sent.Timestamp
	if existing.Kind == "" {
		existing.Kind = waclient.ChatKindFromJID(req.ChatJID)
	}
	if err := s.bundle.Chats.Put(ctx, existing); err != nil {
		s.logger.Warn("upsert chat on send media failed", "chat_jid", req.ChatJID, "err", err)
	}

	return msg, nil
}

// GetMediaRef looks up the media row for a given message ID.
func (s *svc) GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error) {
	if strings.TrimSpace(messageID) == "" {
		return store.MediaRef{}, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Media.GetByMessageID(ctx, messageID)
}

func (s *svc) EditMessage(ctx context.Context, messageID, newText string) (store.Message, error) {
	if strings.TrimSpace(messageID) == "" {
		return store.Message{}, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	if newText == "" {
		return store.Message{}, fmt.Errorf("%w: text is required", ErrInvalidRequest)
	}
	if len(newText) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return store.Message{}, err
	}
	if !s.ownsMessage(existing) {
		return store.Message{}, fmt.Errorf("%w: not the message sender", ErrForbidden)
	}
	if existing.DeletedAt != nil {
		return store.Message{}, fmt.Errorf("%w: message is deleted", ErrForbidden)
	}

	sent, err := s.wa.SendEdit(ctx, existing.ChatJID, messageID, newText)
	if err != nil {
		return store.Message{}, err
	}

	existing.Body = newText
	t := sent.Timestamp
	existing.EditedAt = &t
	if err := s.bundle.Messages.Put(ctx, existing); err != nil {
		s.logger.Warn("persist edit failed", "id", messageID, "err", err)
	}
	return existing, nil
}

func (s *svc) DeleteMessage(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}
	if !s.ownsMessage(existing) {
		return fmt.Errorf("%w: not the message sender", ErrForbidden)
	}
	if existing.DeletedAt != nil {
		return fmt.Errorf("%w: already deleted", ErrForbidden)
	}

	if _, err := s.wa.SendRevoke(ctx, existing.ChatJID, messageID); err != nil {
		return err
	}

	if err := s.bundle.Messages.SoftDelete(ctx, messageID, time.Now()); err != nil {
		s.logger.Warn("soft-delete after revoke failed", "id", messageID, "err", err)
	}
	return nil
}

// ownsMessage reports whether the daemon's current JID matches the message's
// sender JID. Returns false if the daemon isn't currently logged in.
func (s *svc) ownsMessage(m store.Message) bool {
	st := s.wa.Status()
	if st.JID == nil {
		return false
	}
	return *st.JID == m.SenderJID
}

func (s *svc) SendReaction(ctx context.Context, messageID, emoji string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}

	if err := s.wa.SendReaction(ctx, existing.ChatJID, messageID, emoji); err != nil {
		return err
	}

	ourJID := ""
	if st := s.wa.Status(); st.JID != nil {
		ourJID = *st.JID
	}

	if emoji == "" {
		if err := s.bundle.Reactions.Delete(ctx, messageID, ourJID); err != nil {
			s.logger.Warn("clear local reaction failed", "id", messageID, "err", err)
		}
		return nil
	}

	if err := s.bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: messageID,
		SenderJID: ourJID,
		Emoji:     emoji,
		Timestamp: time.Now(),
	}); err != nil {
		s.logger.Warn("persist local reaction failed", "id", messageID, "err", err)
	}
	return nil
}

func (s *svc) ListReactions(ctx context.Context, messageID string) ([]store.Reaction, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Reactions.ListByMessageID(ctx, messageID)
}

func (s *svc) MarkMessageRead(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}

	existing, err := s.bundle.Messages.Get(ctx, messageID)
	if err != nil {
		return err
	}

	if err := s.wa.MarkRead(ctx, existing.ChatJID, existing.SenderJID, messageID, time.Now()); err != nil {
		return err
	}

	chat, err := s.bundle.Chats.Get(ctx, existing.ChatJID)
	if err == nil && chat.UnreadCount > 0 {
		chat.UnreadCount--
		if err := s.bundle.Chats.Put(ctx, chat); err != nil {
			s.logger.Warn("decrement unread on mark-read failed", "chat_jid", existing.ChatJID, "err", err)
		}
	}
	return nil
}

func (s *svc) SendTyping(ctx context.Context, chatJID, state string) error {
	if strings.TrimSpace(chatJID) == "" {
		return fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if state != "composing" && state != "paused" {
		return fmt.Errorf("%w: state must be composing or paused", ErrInvalidRequest)
	}
	return s.wa.SendChatPresence(ctx, chatJID, state)
}

func (s *svc) ListReceipts(ctx context.Context, messageID string) ([]store.Receipt, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Receipts.ListByMessageID(ctx, messageID)
}

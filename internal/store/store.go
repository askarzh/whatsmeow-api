// Package store defines the daemon's app-level persistence interfaces. The
// SQLite implementation lives in internal/store/sqlite; Plan 10 will add a
// Postgres impl in internal/store/postgres.
package store

import (
	"context"
	"time"
)

// Chat is one conversation — direct or group.
type Chat struct {
	JID         string
	Name        string
	Kind        string // "user" | "group" | "broadcast"
	LastMsgAt   time.Time
	UnreadCount int
	Archived    bool
}

// Message is one persisted message in a chat.
type Message struct {
	ID         string // whatsmeow's native id (e.g. "3EB05ABC...")
	ChatJID    string
	SenderJID  string
	Timestamp  time.Time
	Kind       string // "text" | "image" | "video" | "audio" | "document" | "sticker" | "system"
	Body       string
	ReplyTo    string // empty if not a reply
	EditedAt   *time.Time
	DeletedAt  *time.Time
	RawMeta    string // JSON-encoded passthrough of the whatsmeow event
}

// Contact is a known WhatsApp identity.
type Contact struct {
	JID          string
	PushName     string
	FullName     string
	BusinessName string
}

// MediaRef points to an on-disk attachment for a message.
type MediaRef struct {
	MessageID string
	MIME      string
	Size      int64
	SHA256    string
	Path      string
}

// Reaction is an emoji reaction on a message. PK is (MessageID, SenderJID) —
// each user has at most one current reaction per message.
type Reaction struct {
	MessageID string
	SenderJID string
	Emoji     string
	Timestamp time.Time
}

// EventLogEntry is one row in events_log, used by SSE Last-Event-ID resume.
type EventLogEntry struct {
	Seq     int64
	Time    time.Time
	Type    string
	Payload string // JSON-encoded
}

// ChatStore manages the chats table.
type ChatStore interface {
	Put(ctx context.Context, c Chat) error
	Get(ctx context.Context, jid string) (Chat, error)
	List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]Chat, error)
	SetArchived(ctx context.Context, jid string, archived bool) error
	Count(ctx context.Context) (int, error)
	TotalUnread(ctx context.Context) (int, error)
}

// MessageStore manages the messages table and FTS index.
type MessageStore interface {
	Put(ctx context.Context, m Message) error
	Get(ctx context.Context, id string) (Message, error)
	ListByChat(ctx context.Context, chatJID string, limit int, beforeTS time.Time) ([]Message, error)
	Search(ctx context.Context, query string, limit int) ([]Message, error)
	SoftDelete(ctx context.Context, id string, when time.Time) error
	Count(ctx context.Context) (int, error)
}

// ContactStore manages the contacts table.
type ContactStore interface {
	Put(ctx context.Context, c Contact) error
	Get(ctx context.Context, jid string) (Contact, error)
	List(ctx context.Context) ([]Contact, error)
	Search(ctx context.Context, query string, limit int) ([]Contact, error)
	Count(ctx context.Context) (int, error)
}

// MediaStore manages the media table.
type MediaStore interface {
	Put(ctx context.Context, m MediaRef) error
	GetByMessageID(ctx context.Context, messageID string) (MediaRef, error)
}

// EventsLog manages the bounded events_log used for SSE resume.
type EventsLog interface {
	Append(ctx context.Context, entry EventLogEntry) (int64, error)
	SinceSeq(ctx context.Context, seq int64, limit int) ([]EventLogEntry, error)
}

// KV is small daemon state.
type KV interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// ReactionStore manages the reactions table.
type ReactionStore interface {
	Put(ctx context.Context, r Reaction) error
	Delete(ctx context.Context, messageID, senderJID string) error
	ListByMessageID(ctx context.Context, messageID string) ([]Reaction, error)
}

// Receipt is one inbound acknowledgement of one of our outbound messages.
// PK is (MessageID, ReaderJID, Type) — same reader can have separate
// "delivered" → "read" → "played" rows.
type Receipt struct {
	MessageID string
	ReaderJID string
	Type      string // "delivered" | "read" | "played"
	Timestamp time.Time
}

// ReceiptStore manages the receipts table.
type ReceiptStore interface {
	Put(ctx context.Context, r Receipt) error
	ListByMessageID(ctx context.Context, messageID string) ([]Receipt, error)
}

// Bundle aggregates the per-domain interfaces. Constructed by the SQLite store
// (or, in Plan 10, the Postgres store) and passed into HTTP handlers via Deps.
type Bundle struct {
	Chats     ChatStore
	Messages  MessageStore
	Contacts  ContactStore
	Media     MediaStore
	Events    EventsLog
	KV        KV
	Reactions ReactionStore // Plan 07b
	Receipts  ReceiptStore  // Plan 07c
}

// ErrNotFound is returned by Get* methods when the key is absent.
var ErrNotFound = sentinelError("store: not found")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

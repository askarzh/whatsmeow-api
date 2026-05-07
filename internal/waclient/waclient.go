// Package waclient is the only package that imports whatsmeow. It owns the
// *whatsmeow.Client, registers event handlers, and translates whatsmeow types
// into the domain types used by the rest of the daemon.
package waclient

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Status is the daemon's view of the current WhatsApp connection.
type Status struct {
	Connected bool
	JID       *string
	PushName  *string
	Since     *time.Time
}

// QREvent is one frame of the QR-login stream.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// PairEvent is one frame of the phone-pair-login stream.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// Sent is the envelope returned by SendText: enough information for the caller
// to persist the message as our own outbound row.
type Sent struct {
	ID        string
	Timestamp time.Time
	SenderJID string
}

// IncomingMessage is one received message translated out of whatsmeow's
// *events.Message. Plan 04 covers text and media-kind messages; protocol /
// system events (group state changes etc.) are filtered at the adapter and
// never reach the handler.
type IncomingMessage struct {
	ID        string
	ChatJID   string
	ChatKind  string // "user" | "group" | "broadcast" | "newsletter"
	SenderJID string
	Timestamp time.Time
	Kind      string // "text" | "image" | "video" | "audio" | "document" | "sticker"
	Body      string // empty for non-text
	PushName  string
	// Plan 06: closure that downloads the media bytes for this message.
	// nil for non-media kinds. Adapter populates with a closure capturing the
	// proto submessage; service calls it from a goroutine.
	MediaDownloader func(ctx context.Context) ([]byte, string /* mime */, error)

	// Plan 07a — set when this event is an edit or revoke of a previous message.
	// Mutually exclusive (ProtocolMessage is one variant or the other).
	EditOfID   string
	RevokeOfID string
}

// WAClient is the abstraction over whatsmeow used by the rest of the daemon.
type WAClient interface {
	Status() Status
	Resume(ctx context.Context) error
	LoginQR(ctx context.Context) (<-chan QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
	Logout(ctx context.Context) error
	Close() error

	// Plan 04 additions. CHANGED in Plan 07a: replyTo parameter added (empty string = not a reply).
	SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error)
	OnIncomingMessage(handler func(IncomingMessage))

	// Plan 06
	SendMedia(ctx context.Context, chatJID, kind, caption, filename, mime string, body []byte) (Sent, error)

	// Plan 07a
	SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error)
	SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error)
}

// Sentinel errors so callers can distinguish failure modes without parsing strings.
var (
	ErrLoginInProgress = errors.New("waclient: login already in progress")
	ErrAlreadyLoggedIn = errors.New("waclient: already logged in")
	ErrNotLoggedIn     = errors.New("waclient: not logged in")
	ErrNotConnected    = errors.New("waclient: not connected")
)

var phoneRE = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// IsValidPhoneNumber checks that s looks like an E.164 number.
func IsValidPhoneNumber(s string) bool {
	return phoneRE.MatchString(s)
}

// ChatKindFromJID classifies a WhatsApp JID by its server suffix.
func ChatKindFromJID(jid string) string {
	switch {
	case strings.HasSuffix(jid, "@s.whatsapp.net"):
		return "user"
	case strings.HasSuffix(jid, "@g.us"):
		return "group"
	case strings.HasSuffix(jid, "@broadcast"):
		return "broadcast"
	case strings.HasSuffix(jid, "@newsletter"):
		return "newsletter"
	default:
		return "unknown"
	}
}

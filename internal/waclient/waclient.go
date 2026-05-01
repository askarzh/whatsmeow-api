// Package waclient is the only package that imports whatsmeow. It owns the
// *whatsmeow.Client, registers event handlers, and translates whatsmeow types
// into the domain types used by the rest of the daemon.
package waclient

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// Status is the daemon's view of the current WhatsApp connection.
type Status struct {
	Connected bool
	JID       *string
	PushName  *string
	Since     *time.Time
}

// QREvent is one frame of the QR-login stream. While streaming, Code is set
// and Terminal is false. The final event has Terminal=true and Outcome set.
type QREvent struct {
	Code     string
	Terminal bool
	Outcome  string // "success" | "timeout" | "err-..." | "ctx-cancelled"
}

// PairEvent is one frame of the phone-pair-login stream. The first event
// carries Code (the 8-char pairing code). The terminal event has Terminal=true.
type PairEvent struct {
	Code     string
	Terminal bool
	Outcome  string
}

// WAClient is the abstraction over whatsmeow used by the rest of the daemon.
type WAClient interface {
	Status() Status
	Resume(ctx context.Context) error
	LoginQR(ctx context.Context) (<-chan QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
	Logout(ctx context.Context) error
	Close() error
}

// Sentinel errors so callers can distinguish failure modes without parsing strings.
var (
	ErrLoginInProgress = errors.New("waclient: login already in progress")
	ErrAlreadyLoggedIn = errors.New("waclient: already logged in")
	ErrNotLoggedIn     = errors.New("waclient: not logged in")
)

var phoneRE = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// IsValidPhoneNumber checks that s looks like an E.164 number (a leading '+'
// followed by 6-15 digits, no spaces or punctuation).
func IsValidPhoneNumber(s string) bool {
	return phoneRE.MatchString(s)
}

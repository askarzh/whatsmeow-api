package waclient

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"

	// Database drivers — imported for side-effects so database/sql can find them.
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Adapter is the production WAClient backed by *whatsmeow.Client.
type Adapter struct {
	container *sqlstore.Container
	logger    *slog.Logger

	mu              sync.Mutex
	client          *whatsmeow.Client
	loginInProgress bool
	lastConnectedAt time.Time

	pairCh chan string // signaled with outcome by event handler during phone pair
}

// NewAdapter constructs an Adapter. The container must already be initialized
// (use OpenSQLite or OpenPostgres).
func NewAdapter(container *sqlstore.Container, logger *slog.Logger) *Adapter {
	return &Adapter{container: container, logger: logger}
}

// OpenSQLite opens (or creates) the whatsmeow session DB at path.
func OpenSQLite(ctx context.Context, path string, logger *slog.Logger) (*sqlstore.Container, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	c, err := sqlstore.New(ctx, "sqlite", dsn, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlstore sqlite: %w", err)
	}
	return c, nil
}

// OpenPostgres opens a Postgres-backed session store using a dedicated schema.
// dsn is the canonical Postgres URL; the schema "whatsmeow_session" is created
// if missing and search_path is scoped to it for this connection.
func OpenPostgres(ctx context.Context, dsn string, logger *slog.Logger) (*sqlstore.Container, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS whatsmeow_session`); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO whatsmeow_session, public`); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	c := sqlstore.NewWithDB(db, "postgres", nil)
	if err := c.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("sqlstore postgres upgrade: %w", err)
	}
	return c, nil
}

// ensureClient lazily constructs the whatsmeow.Client backed by the existing
// device (or a fresh one if no session is stored).
func (a *Adapter) ensureClient(ctx context.Context) error {
	if a.client != nil {
		return nil
	}
	device, err := a.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	a.client = whatsmeow.NewClient(device, nil)
	a.client.AddEventHandler(a.onEvent)
	return nil
}

// onEvent is the single subscription point for whatsmeow events.
func (a *Adapter) onEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		a.mu.Lock()
		a.lastConnectedAt = time.Now()
		a.mu.Unlock()
		a.logger.Info("wa connected")
	case *events.Disconnected:
		a.logger.Info("wa disconnected")
	case *events.LoggedOut:
		a.logger.Info("wa logged out", "reason", evt.Reason.String())
	case *events.PairSuccess:
		a.logger.Info("wa pair success", "jid", evt.ID.String())
		a.signalPair("success")
	case *events.PairError:
		a.logger.Warn("wa pair error", "err", evt.Error.Error())
		a.signalPair("err-" + evt.Error.Error())
	}
}

func (a *Adapter) signalPair(outcome string) {
	a.mu.Lock()
	ch := a.pairCh
	a.pairCh = nil
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- outcome:
		default:
		}
	}
}

// Status returns the current view of the connection.
func (a *Adapter) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil ||
		!a.client.IsConnected() || !a.client.IsLoggedIn() {
		return Status{}
	}
	jid := a.client.Store.ID.String()
	push := a.client.Store.PushName
	since := a.lastConnectedAt
	return Status{
		Connected: true,
		JID:       &jid,
		PushName:  &push,
		Since:     &since,
	}
}

// Resume connects an existing session if one is stored. No-op if none.
func (a *Adapter) Resume(ctx context.Context) error {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.client.Store.ID == nil {
		a.mu.Unlock()
		return nil // no saved session
	}
	a.mu.Unlock()
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// LoginQR starts a fresh QR pairing flow.
func (a *Adapter) LoginQR(ctx context.Context) (<-chan QREvent, error) {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.mu.Unlock()

	qrCh, err := a.client.GetQRChannel(ctx)
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("get qr channel: %w", err)
	}
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}

	out := make(chan QREvent, 4)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.mu.Unlock()
		}()
		for evt := range qrCh {
			switch evt.Event {
			case "code":
				select {
				case out <- QREvent{Code: evt.Code}:
				case <-ctx.Done():
					out <- QREvent{Terminal: true, Outcome: "ctx-cancelled"}
					return
				}
			case "success":
				out <- QREvent{Terminal: true, Outcome: "success"}
				return
			case "timeout":
				out <- QREvent{Terminal: true, Outcome: "timeout"}
				return
			default:
				out <- QREvent{Terminal: true, Outcome: "err-" + evt.Event}
				return
			}
		}
	}()
	return out, nil
}

// LoginPhone starts a phone-pair flow. The first PairEvent carries the 8-char
// pair code; the terminal event arrives when the user enters the code (or the
// 2-minute window expires).
func (a *Adapter) LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error) {
	if !IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}

	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.pairCh = make(chan string, 1)
	pairCh := a.pairCh
	a.mu.Unlock()

	// Connect first so PairPhone can send the linking request.
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}
	code, err := a.client.PairPhone(ctx, phoneNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("pair phone: %w", err)
	}

	out := make(chan PairEvent, 2)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.pairCh = nil
			a.mu.Unlock()
		}()
		out <- PairEvent{Code: code}
		select {
		case outcome := <-pairCh:
			out <- PairEvent{Terminal: true, Outcome: outcome}
		case <-ctx.Done():
			out <- PairEvent{Terminal: true, Outcome: "ctx-cancelled"}
		case <-time.After(150 * time.Second):
			out <- PairEvent{Terminal: true, Outcome: "timeout"}
		}
	}()
	return out, nil
}

// Logout disconnects and tells WhatsApp to invalidate the session.
func (a *Adapter) Logout(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil || !a.client.IsLoggedIn() {
		return ErrNotLoggedIn
	}
	if err := a.client.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Close disconnects without logging out. Called on daemon shutdown.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil && a.client.IsConnected() {
		a.client.Disconnect()
	}
	return nil
}

// compile-time interface check
var _ WAClient = (*Adapter)(nil)

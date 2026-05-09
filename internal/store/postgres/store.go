// Package postgres is the Postgres implementation of the app store. It mirrors
// the SQLite package's shape: New() opens a connection, runs migrations, and
// returns a *Store that exposes a store.Bundle.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migpgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // register pgx driver under "pgx"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/migrations"
)

// Store is the Postgres implementation of the app store. It holds the
// underlying *sql.DB plus per-domain sub-stores; Bundle() exposes them as the
// interface types defined in package store.
//
// Per-domain sub-store fields are wired in Tasks 4-6. Until then they remain
// nil and Bundle() returns a struct with nil interface fields.
type Store struct {
	db *sql.DB

	chats     store.ChatStore
	messages  store.MessageStore
	contacts  store.ContactStore
	media     store.MediaStore
	events    store.EventsLog
	kv        store.KV
	reactions store.ReactionStore
	receipts  store.ReceiptStore
}

// New opens a Postgres connection at dsn, runs all pending migrations, and
// returns a *Store. The DSN is the standard libpq URL or keyword-value form
// (pgx accepts both).
func New(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open pgx: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{db: db}
	// Per-domain sub-stores are wired in Tasks 4-6.
	return s, nil
}

// Close releases the underlying *sql.DB.
func (s *Store) Close() error {
	return s.db.Close()
}

// Bundle returns the store interfaces used by the service layer. Until
// Tasks 4-6 wire the sub-stores, callers should not invoke methods on these
// fields — they will be nil.
func (s *Store) Bundle() store.Bundle {
	return store.Bundle{
		Chats:     s.chats,
		Messages:  s.messages,
		Contacts:  s.contacts,
		Media:     s.media,
		Events:    s.events,
		KV:        s.kv,
		Reactions: s.reactions,
		Receipts:  s.receipts,
	}
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrations.Postgres(), ".")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	driver, err := migpgx.WithInstance(db, &migpgx.Config{})
	if err != nil {
		return fmt.Errorf("migrations driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx", driver)
	if err != nil {
		return fmt.Errorf("migrations new: %w", err)
	}
	// Do NOT call m.Close(): the migrate driver's Close() closes the underlying
	// *sql.DB, which must stay open for the store's lifetime.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations up: %w", err)
	}
	return nil
}

// Package sqlite implements the app-store interfaces from internal/store
// against a SQLite database file. Drivers are registered via blank imports
// in the store constructor.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/store/migrations"

	"github.com/golang-migrate/migrate/v4"
	migsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// Store is the SQLite implementation of the app store. It holds the underlying
// *sql.DB plus per-domain sub-stores; Bundle() exposes them as the interface
// types defined in package store.
type Store struct {
	db *sql.DB

	chats     *ChatStore
	messages  *MessageStore
	contacts  *ContactStore
	media     *MediaStore
	events    *EventsLog
	kv        *KVStore
	reactions *ReactionStore // Plan 07b
	receipts  *ReceiptStore  // Plan 07c
}

// New opens (or creates) the SQLite database at path, runs all pending
// migrations, and returns a *Store ready for use.
func New(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{db: db}
	s.chats = &ChatStore{db: db}
	s.messages = &MessageStore{db: db}
	s.contacts = &ContactStore{db: db}
	s.media = &MediaStore{db: db}
	s.events = &EventsLog{db: db}
	s.kv = &KVStore{db: db}
	s.reactions = &ReactionStore{db: db}
	s.receipts = &ReceiptStore{db: db}
	return s, nil
}

// Close releases the underlying *sql.DB.
func (s *Store) Close() error {
	return s.db.Close()
}

// Bundle returns the store interfaces for use by HTTP handlers.
func (s *Store) Bundle() store.Bundle {
	return store.Bundle{
		Chats:     s.chats,
		Messages:  s.messages,
		Contacts:  s.contacts,
		Media:     s.media,
		Events:    s.events,
		KV:        s.kv,
		Reactions: s.reactions, // Plan 07b
		Receipts:  s.receipts,  // Plan 07c
	}
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrations.SQLite(), ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	driver, err := migsqlite.WithInstance(db, &migsqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrate sqlite driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("migrate.NewWithInstance: %w", err)
	}
	// Do NOT call m.Close(): the sqlite database driver's Close() closes the
	// underlying *sql.DB, which must stay open for the store's lifetime.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

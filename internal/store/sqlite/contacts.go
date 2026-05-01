package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ContactStore struct{ db *sql.DB }

const contactColumns = `jid, push_name, full_name, business_name`

func (s *ContactStore) Put(ctx context.Context, c store.Contact) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contacts (jid, push_name, full_name, business_name)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			push_name = excluded.push_name,
			full_name = excluded.full_name,
			business_name = excluded.business_name
	`, c.JID, nullableString(c.PushName), nullableString(c.FullName), nullableString(c.BusinessName))
	if err != nil {
		return fmt.Errorf("contacts put: %w", err)
	}
	return nil
}

func (s *ContactStore) Get(ctx context.Context, jid string) (store.Contact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE jid = ?`, jid)
	c, err := scanContact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Contact{}, store.ErrNotFound
	}
	if err != nil {
		return store.Contact{}, fmt.Errorf("contacts get: %w", err)
	}
	return c, nil
}

func (s *ContactStore) List(ctx context.Context) ([]store.Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+` FROM contacts ORDER BY jid ASC`)
	if err != nil {
		return nil, fmt.Errorf("contacts list: %w", err)
	}
	defer rows.Close()
	var out []store.Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, fmt.Errorf("contacts list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanContact(s scanner) (store.Contact, error) {
	var (
		c            store.Contact
		pushName     sql.NullString
		fullName     sql.NullString
		businessName sql.NullString
	)
	if err := s.Scan(&c.JID, &pushName, &fullName, &businessName); err != nil {
		return store.Contact{}, err
	}
	c.PushName = pushName.String
	c.FullName = fullName.String
	c.BusinessName = businessName.String
	return c, nil
}

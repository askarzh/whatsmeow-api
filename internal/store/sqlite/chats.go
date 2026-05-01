package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ChatStore struct{ db *sql.DB }

const chatColumns = `jid, name, kind, last_msg_at, unread_count, archived`

func (s *ChatStore) Put(ctx context.Context, c store.Chat) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (jid, name, kind, last_msg_at, unread_count, archived)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			last_msg_at = excluded.last_msg_at,
			unread_count = excluded.unread_count,
			archived = excluded.archived
	`,
		c.JID, c.Name, c.Kind, unixOrNil(c.LastMsgAt), c.UnreadCount, boolToInt(c.Archived),
	)
	if err != nil {
		return fmt.Errorf("chats put: %w", err)
	}
	return nil
}

func (s *ChatStore) Get(ctx context.Context, jid string) (store.Chat, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE jid = ?`, jid)
	c, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Chat{}, store.ErrNotFound
	}
	if err != nil {
		return store.Chat{}, fmt.Errorf("chats get: %w", err)
	}
	return c, nil
}

func (s *ChatStore) List(ctx context.Context, includeArchived bool) ([]store.Chat, error) {
	q := `SELECT ` + chatColumns + ` FROM chats`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	q += ` ORDER BY last_msg_at DESC NULLS LAST, jid ASC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("chats list: %w", err)
	}
	defer rows.Close()
	var out []store.Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, fmt.Errorf("chats list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *ChatStore) SetArchived(ctx context.Context, jid string, archived bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET archived = ? WHERE jid = ?`, boolToInt(archived), jid)
	if err != nil {
		return fmt.Errorf("chats set_archived: %w", err)
	}
	return nil
}

// scanner matches both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanChat(s scanner) (store.Chat, error) {
	var (
		c           store.Chat
		name        sql.NullString
		lastMsgAt   sql.NullInt64
		archivedInt int
	)
	if err := s.Scan(&c.JID, &name, &c.Kind, &lastMsgAt, &c.UnreadCount, &archivedInt); err != nil {
		return store.Chat{}, err
	}
	c.Name = name.String
	c.Archived = archivedInt != 0
	if lastMsgAt.Valid {
		c.LastMsgAt = unixToTime(lastMsgAt.Int64)
	}
	return c, nil
}

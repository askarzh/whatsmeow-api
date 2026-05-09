package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ChatStore struct{ db *sql.DB }

const chatColumns = `jid, name, kind, last_msg_at, unread_count, archived`

func (s *ChatStore) Put(ctx context.Context, c store.Chat) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (jid, name, kind, last_msg_at, unread_count, archived)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(jid) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			last_msg_at = excluded.last_msg_at,
			unread_count = excluded.unread_count,
			archived = excluded.archived
	`,
		c.JID, c.Name, c.Kind, timeOrNil(c.LastMsgAt), c.UnreadCount, c.Archived,
	)
	if err != nil {
		return fmt.Errorf("chats put: %w", err)
	}
	return nil
}

func (s *ChatStore) Get(ctx context.Context, jid string) (store.Chat, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE jid = $1`, jid)
	c, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Chat{}, store.ErrNotFound
	}
	if err != nil {
		return store.Chat{}, fmt.Errorf("chats get: %w", err)
	}
	return c, nil
}

func (s *ChatStore) List(ctx context.Context, beforeMsgAt time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	q := `SELECT ` + chatColumns + ` FROM chats`
	var conds []string
	var args []any
	if !includeArchived {
		conds = append(conds, `archived = FALSE`)
	}
	if !beforeMsgAt.IsZero() {
		conds = append(conds, fmt.Sprintf(`(last_msg_at IS NOT NULL AND last_msg_at < $%d)`, len(args)+1))
		args = append(args, beforeMsgAt)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += fmt.Sprintf(` ORDER BY last_msg_at DESC NULLS LAST, jid ASC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
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
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET archived = $1 WHERE jid = $2`, archived, jid)
	if err != nil {
		return fmt.Errorf("chats set_archived: %w", err)
	}
	return nil
}

func (s *ChatStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&n); err != nil {
		return 0, fmt.Errorf("chats count: %w", err)
	}
	return n, nil
}

func (s *ChatStore) TotalUnread(ctx context.Context) (int, error) {
	var n sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(unread_count), 0) FROM chats`).Scan(&n); err != nil {
		return 0, fmt.Errorf("chats total_unread: %w", err)
	}
	return int(n.Int64), nil
}

func scanChat(s scanner) (store.Chat, error) {
	var (
		c         store.Chat
		name      sql.NullString
		lastMsgAt sql.NullTime
	)
	if err := s.Scan(&c.JID, &name, &c.Kind, &lastMsgAt, &c.UnreadCount, &c.Archived); err != nil {
		return store.Chat{}, err
	}
	c.Name = name.String
	if lastMsgAt.Valid {
		c.LastMsgAt = lastMsgAt.Time.UTC()
	}
	return c, nil
}

var _ store.ChatStore = (*ChatStore)(nil)

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MessageStore struct{ db *sql.DB }

const messageColumns = `id, chat_jid, sender_jid, ts, kind, body, reply_to, edited_at, deleted_at, raw_meta`

func (s *MessageStore) Put(ctx context.Context, m store.Message) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, chat_jid, sender_jid, ts, kind, body, reply_to, edited_at, deleted_at, raw_meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			chat_jid = excluded.chat_jid,
			sender_jid = excluded.sender_jid,
			ts = excluded.ts,
			kind = excluded.kind,
			body = excluded.body,
			reply_to = excluded.reply_to,
			edited_at = excluded.edited_at,
			deleted_at = excluded.deleted_at,
			raw_meta = excluded.raw_meta
	`,
		m.ID, m.ChatJID, m.SenderJID, m.Timestamp.Unix(), m.Kind,
		nullableString(m.Body), nullableString(m.ReplyTo),
		ptrUnix(m.EditedAt), ptrUnix(m.DeletedAt),
		nullableString(m.RawMeta),
	)
	if err != nil {
		return fmt.Errorf("messages put: %w", err)
	}
	return nil
}

func (s *MessageStore) Get(ctx context.Context, id string) (store.Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Message{}, store.ErrNotFound
	}
	if err != nil {
		return store.Message{}, fmt.Errorf("messages get: %w", err)
	}
	return m, nil
}

func (s *MessageStore) ListByChat(ctx context.Context, chatJID string, limit int, beforeTS time.Time) ([]store.Message, error) {
	q := `SELECT ` + messageColumns + ` FROM messages WHERE chat_jid = ? AND deleted_at IS NULL`
	args := []any{chatJID}
	if !beforeTS.IsZero() {
		q += ` AND ts < ?`
		args = append(args, beforeTS.Unix())
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("messages list_by_chat: %w", err)
	}
	defer rows.Close()
	var out []store.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("messages list_by_chat scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MessageStore) Search(ctx context.Context, query string, limit int) ([]store.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixCols(messageColumns, "m.")+`
		FROM messages_fts f
		JOIN messages m ON m.rowid = f.rowid
		WHERE messages_fts MATCH ? AND m.deleted_at IS NULL
		ORDER BY m.ts DESC
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("messages search: %w", err)
	}
	defer rows.Close()
	var out []store.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("messages search scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MessageStore) SoftDelete(ctx context.Context, id string, when time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET deleted_at = ? WHERE id = ?`, when.Unix(), id)
	if err != nil {
		return fmt.Errorf("messages soft_delete: %w", err)
	}
	return nil
}

func scanMessage(s scanner) (store.Message, error) {
	var (
		m         store.Message
		body      sql.NullString
		replyTo   sql.NullString
		editedAt  sql.NullInt64
		deletedAt sql.NullInt64
		rawMeta   sql.NullString
		ts        int64
	)
	if err := s.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &ts, &m.Kind, &body, &replyTo, &editedAt, &deletedAt, &rawMeta); err != nil {
		return store.Message{}, err
	}
	m.Timestamp = unixToTime(ts)
	m.Body = body.String
	m.ReplyTo = replyTo.String
	m.RawMeta = rawMeta.String
	if editedAt.Valid {
		t := unixToTime(editedAt.Int64)
		m.EditedAt = &t
	}
	if deletedAt.Valid {
		t := unixToTime(deletedAt.Int64)
		m.DeletedAt = &t
	}
	return m, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ptrUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// prefixCols rewrites a column list like "a, b, c" into "p.a, p.b, p.c".
func prefixCols(cols, prefix string) string {
	out := ""
	for i, r := range cols {
		switch {
		case i == 0 || cols[i-1] == ' ':
			out += prefix + string(r)
		default:
			out += string(r)
		}
	}
	return out
}

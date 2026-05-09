package postgres

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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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
		m.ID, m.ChatJID, m.SenderJID, m.Timestamp, m.Kind,
		nullableString(m.Body), nullableString(m.ReplyTo),
		ptrTimeOrNil(m.EditedAt), ptrTimeOrNil(m.DeletedAt),
		nullableString(m.RawMeta),
	)
	if err != nil {
		return fmt.Errorf("messages put: %w", err)
	}
	return nil
}

func (s *MessageStore) Get(ctx context.Context, id string) (store.Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = $1`, id)
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
	q := `SELECT ` + messageColumns + ` FROM messages WHERE chat_jid = $1 AND deleted_at IS NULL`
	args := []any{chatJID}
	if !beforeTS.IsZero() {
		q += fmt.Sprintf(` AND ts < $%d`, len(args)+1)
		args = append(args, beforeTS)
	}
	q += fmt.Sprintf(` ORDER BY ts DESC LIMIT $%d`, len(args)+1)
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

// Search performs full-text search using the body_tsv generated tsvector
// column with a GIN index. Ranking uses ts_rank, which differs from SQLite's
// FTS5 BM25 — the storesuite assertions are tolerant of dialect-specific
// ordering (set membership rather than exact order).
func (s *MessageStore) Search(ctx context.Context, query string, limit int) ([]store.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+messageColumns+`
		FROM messages
		WHERE body_tsv @@ plainto_tsquery('simple', $1)
		  AND deleted_at IS NULL
		ORDER BY ts_rank(body_tsv, plainto_tsquery('simple', $1)) DESC, ts DESC
		LIMIT $2
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
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET deleted_at = $1 WHERE id = $2`, when, id)
	if err != nil {
		return fmt.Errorf("messages soft_delete: %w", err)
	}
	return nil
}

func (s *MessageStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("messages count: %w", err)
	}
	return n, nil
}

func scanMessage(s scanner) (store.Message, error) {
	var (
		m         store.Message
		body      sql.NullString
		replyTo   sql.NullString
		editedAt  sql.NullTime
		deletedAt sql.NullTime
		rawMeta   sql.NullString
	)
	if err := s.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.Timestamp, &m.Kind, &body, &replyTo, &editedAt, &deletedAt, &rawMeta); err != nil {
		return store.Message{}, err
	}
	m.Timestamp = m.Timestamp.UTC()
	m.Body = body.String
	m.ReplyTo = replyTo.String
	m.RawMeta = rawMeta.String
	if editedAt.Valid {
		t := editedAt.Time.UTC()
		m.EditedAt = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time.UTC()
		m.DeletedAt = &t
	}
	return m, nil
}

var _ store.MessageStore = (*MessageStore)(nil)

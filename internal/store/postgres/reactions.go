package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ReactionStore struct{ db *sql.DB }

const reactionColumns = `message_id, sender_jid, emoji, ts`

func (s *ReactionStore) Put(ctx context.Context, r store.Reaction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reactions (message_id, sender_jid, emoji, ts)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT(message_id, sender_jid) DO UPDATE SET
			emoji = excluded.emoji,
			ts    = excluded.ts
	`, r.MessageID, r.SenderJID, r.Emoji, r.Timestamp)
	if err != nil {
		return fmt.Errorf("reactions put: %w", err)
	}
	return nil
}

func (s *ReactionStore) Delete(ctx context.Context, messageID, senderJID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM reactions WHERE message_id = $1 AND sender_jid = $2`,
		messageID, senderJID)
	if err != nil {
		return fmt.Errorf("reactions delete: %w", err)
	}
	return nil
}

func (s *ReactionStore) ListByMessageID(ctx context.Context, messageID string) ([]store.Reaction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+reactionColumns+` FROM reactions WHERE message_id = $1 ORDER BY sender_jid ASC`,
		messageID)
	if err != nil {
		return nil, fmt.Errorf("reactions list: %w", err)
	}
	defer rows.Close()
	out := make([]store.Reaction, 0)
	for rows.Next() {
		var (
			r  store.Reaction
			ts sql.NullTime
		)
		if err := rows.Scan(&r.MessageID, &r.SenderJID, &r.Emoji, &ts); err != nil {
			return nil, fmt.Errorf("reactions list scan: %w", err)
		}
		if ts.Valid {
			r.Timestamp = ts.Time.UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ store.ReactionStore = (*ReactionStore)(nil)

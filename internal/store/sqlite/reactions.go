package sqlite

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
		VALUES (?, ?, ?, ?)
		ON CONFLICT(message_id, sender_jid) DO UPDATE SET
			emoji = excluded.emoji,
			ts    = excluded.ts
	`, r.MessageID, r.SenderJID, r.Emoji, r.Timestamp.Unix())
	if err != nil {
		return fmt.Errorf("reactions put: %w", err)
	}
	return nil
}

func (s *ReactionStore) Delete(ctx context.Context, messageID, senderJID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM reactions WHERE message_id = ? AND sender_jid = ?`,
		messageID, senderJID)
	if err != nil {
		return fmt.Errorf("reactions delete: %w", err)
	}
	return nil
}

func (s *ReactionStore) ListByMessageID(ctx context.Context, messageID string) ([]store.Reaction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+reactionColumns+` FROM reactions WHERE message_id = ? ORDER BY sender_jid ASC`,
		messageID)
	if err != nil {
		return nil, fmt.Errorf("reactions list: %w", err)
	}
	defer rows.Close()
	out := make([]store.Reaction, 0)
	for rows.Next() {
		var (
			r  store.Reaction
			ts int64
		)
		if err := rows.Scan(&r.MessageID, &r.SenderJID, &r.Emoji, &ts); err != nil {
			return nil, fmt.Errorf("reactions list scan: %w", err)
		}
		r.Timestamp = unixToTime(ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

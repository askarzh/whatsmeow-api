package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ReceiptStore struct{ db *sql.DB }

const receiptColumns = `message_id, reader_jid, type, ts`

func (s *ReceiptStore) Put(ctx context.Context, r store.Receipt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO receipts (message_id, reader_jid, type, ts)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(message_id, reader_jid, type) DO UPDATE SET
			ts = excluded.ts
	`, r.MessageID, r.ReaderJID, r.Type, r.Timestamp.Unix())
	if err != nil {
		return fmt.Errorf("receipts put: %w", err)
	}
	return nil
}

func (s *ReceiptStore) ListByMessageID(ctx context.Context, messageID string) ([]store.Receipt, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+receiptColumns+` FROM receipts WHERE message_id = ? ORDER BY reader_jid ASC, type ASC`,
		messageID)
	if err != nil {
		return nil, fmt.Errorf("receipts list: %w", err)
	}
	defer rows.Close()
	out := make([]store.Receipt, 0)
	for rows.Next() {
		var (
			r  store.Receipt
			ts int64
		)
		if err := rows.Scan(&r.MessageID, &r.ReaderJID, &r.Type, &ts); err != nil {
			return nil, fmt.Errorf("receipts list scan: %w", err)
		}
		r.Timestamp = unixToTime(ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

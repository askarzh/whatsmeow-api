package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type EventsLog struct{ db *sql.DB }

func (s *EventsLog) Append(ctx context.Context, e store.EventLogEntry) (int64, error) {
	const stmt = `
		INSERT INTO events_log (ts, type, payload)
		VALUES ($1, $2, $3)
		RETURNING seq
	`
	var seq int64
	if err := s.db.QueryRowContext(ctx, stmt, e.Time, e.Type, e.Payload).Scan(&seq); err != nil {
		return 0, fmt.Errorf("events_log append: %w", err)
	}
	return seq, nil
}

func (s *EventsLog) SinceSeq(ctx context.Context, seq int64, limit int) ([]store.EventLogEntry, error) {
	const stmt = `
		SELECT seq, ts, type, payload
		FROM events_log
		WHERE seq > $1
		ORDER BY seq ASC
		LIMIT $2
	`
	rows, err := s.db.QueryContext(ctx, stmt, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("events_log since_seq: %w", err)
	}
	defer rows.Close()
	var out []store.EventLogEntry
	for rows.Next() {
		var (
			e  store.EventLogEntry
			ts sql.NullTime
		)
		if err := rows.Scan(&e.Seq, &ts, &e.Type, &e.Payload); err != nil {
			return nil, fmt.Errorf("events_log since_seq scan: %w", err)
		}
		if ts.Valid {
			e.Time = ts.Time.UTC()
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ store.EventsLog = (*EventsLog)(nil)

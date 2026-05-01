package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type EventsLog struct{ db *sql.DB }

func (s *EventsLog) Append(ctx context.Context, e store.EventLogEntry) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events_log (ts, type, payload) VALUES (?, ?, ?)
	`, e.Time.Unix(), e.Type, e.Payload)
	if err != nil {
		return 0, fmt.Errorf("events_log append: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("events_log last_id: %w", err)
	}
	return id, nil
}

func (s *EventsLog) SinceSeq(ctx context.Context, seq int64, limit int) ([]store.EventLogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, ts, type, payload FROM events_log
		WHERE seq > ? ORDER BY seq ASC LIMIT ?
	`, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("events_log since_seq: %w", err)
	}
	defer rows.Close()
	var out []store.EventLogEntry
	for rows.Next() {
		var (
			e  store.EventLogEntry
			ts int64
		)
		if err := rows.Scan(&e.Seq, &ts, &e.Type, &e.Payload); err != nil {
			return nil, fmt.Errorf("events_log since_seq scan: %w", err)
		}
		e.Time = unixToTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

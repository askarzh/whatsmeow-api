package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type EventsLog struct{ db *sql.DB }

func (s *EventsLog) Append(_ context.Context, _ store.EventLogEntry) (int64, error) {
	return 0, errors.New("not implemented")
}
func (s *EventsLog) SinceSeq(_ context.Context, _ int64, _ int) ([]store.EventLogEntry, error) {
	return nil, errors.New("not implemented")
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MessageStore struct{ db *sql.DB }

func (s *MessageStore) Put(_ context.Context, _ store.Message) error { return errors.New("not implemented") }
func (s *MessageStore) Get(_ context.Context, _ string) (store.Message, error) {
	return store.Message{}, errors.New("not implemented")
}
func (s *MessageStore) ListByChat(_ context.Context, _ string, _ int, _ time.Time) ([]store.Message, error) {
	return nil, errors.New("not implemented")
}
func (s *MessageStore) Search(_ context.Context, _ string, _ int) ([]store.Message, error) {
	return nil, errors.New("not implemented")
}
func (s *MessageStore) SoftDelete(_ context.Context, _ string, _ time.Time) error {
	return errors.New("not implemented")
}

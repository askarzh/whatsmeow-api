package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MediaStore struct{ db *sql.DB }

func (s *MediaStore) Put(_ context.Context, _ store.MediaRef) error                          { return errors.New("not implemented") }
func (s *MediaStore) GetByMessageID(_ context.Context, _ string) (store.MediaRef, error) {
	return store.MediaRef{}, errors.New("not implemented")
}

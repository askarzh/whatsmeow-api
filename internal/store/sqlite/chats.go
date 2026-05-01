package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ChatStore struct{ db *sql.DB }

func (s *ChatStore) Put(_ context.Context, _ store.Chat) error             { return errors.New("not implemented") }
func (s *ChatStore) Get(_ context.Context, _ string) (store.Chat, error)   { return store.Chat{}, errors.New("not implemented") }
func (s *ChatStore) List(_ context.Context, _ bool) ([]store.Chat, error)  { return nil, errors.New("not implemented") }
func (s *ChatStore) SetArchived(_ context.Context, _ string, _ bool) error { return errors.New("not implemented") }

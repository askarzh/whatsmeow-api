package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type ContactStore struct{ db *sql.DB }

func (s *ContactStore) Put(_ context.Context, _ store.Contact) error           { return errors.New("not implemented") }
func (s *ContactStore) Get(_ context.Context, _ string) (store.Contact, error) { return store.Contact{}, errors.New("not implemented") }
func (s *ContactStore) List(_ context.Context) ([]store.Contact, error)        { return nil, errors.New("not implemented") }

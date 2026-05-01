package sqlite

import (
	"context"
	"database/sql"
	"errors"
)

type KVStore struct{ db *sql.DB }

func (s *KVStore) Get(_ context.Context, _ string) (string, error) { return "", errors.New("not implemented") }
func (s *KVStore) Set(_ context.Context, _, _ string) error        { return errors.New("not implemented") }
func (s *KVStore) Delete(_ context.Context, _ string) error        { return errors.New("not implemented") }

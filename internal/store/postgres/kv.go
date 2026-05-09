package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type KVStore struct{ db *sql.DB }

func (s *KVStore) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("kv get: %w", err)
	}
	return v, nil
}

func (s *KVStore) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO kv (key, value) VALUES ($1, $2)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("kv set: %w", err)
	}
	return nil
}

func (s *KVStore) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("kv delete: %w", err)
	}
	return nil
}

var _ store.KV = (*KVStore)(nil)

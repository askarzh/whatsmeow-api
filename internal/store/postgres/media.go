package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/askarzh/whatsmeow-api/internal/store"
)

type MediaStore struct{ db *sql.DB }

const mediaColumns = `message_id, mime, size, sha256, path`

func (s *MediaStore) Put(ctx context.Context, m store.MediaRef) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media (message_id, mime, size, sha256, path)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(message_id) DO UPDATE SET
			mime = excluded.mime,
			size = excluded.size,
			sha256 = excluded.sha256,
			path = excluded.path
	`, m.MessageID, m.MIME, m.Size, m.SHA256, m.Path)
	if err != nil {
		return fmt.Errorf("media put: %w", err)
	}
	return nil
}

func (s *MediaStore) GetByMessageID(ctx context.Context, messageID string) (store.MediaRef, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE message_id = $1`, messageID)
	var m store.MediaRef
	err := row.Scan(&m.MessageID, &m.MIME, &m.Size, &m.SHA256, &m.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return store.MediaRef{}, store.ErrNotFound
	}
	if err != nil {
		return store.MediaRef{}, fmt.Errorf("media get: %w", err)
	}
	return m, nil
}

var _ store.MediaStore = (*MediaStore)(nil)

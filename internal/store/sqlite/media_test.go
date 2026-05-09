package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
	"github.com/stretchr/testify/require"
)

func TestMediaPutGet(t *testing.T) {
	storesuite.RunMediaPutGet(t, newTestStore(t).Bundle())
}

func TestMediaGetNotFound(t *testing.T) {
	storesuite.RunMediaGetNotFound(t, newTestStore(t).Bundle().Media)
}

func TestMediaCascadesOnMessageDelete(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	storesuite.RunMediaCascadesOnMessageDelete(t, s.Bundle(), func(messageID string) {
		rawDelete(t, dbPath, messageID)
	})
}

// rawDelete issues a hard DELETE through a sibling sql.DB so we can exercise
// the FK cascade (the public MessageStore only soft-deletes).
func rawDelete(t *testing.T, dbPath string, msgID string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, msgID)
	require.NoError(t, err)
}

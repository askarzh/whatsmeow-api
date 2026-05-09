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

func TestReceiptPutGetList(t *testing.T) {
	storesuite.RunReceiptPutGetList(t, newTestStore(t).Bundle())
}

func TestReceiptPutIsUpsert(t *testing.T) {
	storesuite.RunReceiptPutIsUpsert(t, newTestStore(t).Bundle())
}

func TestReceiptListEmpty(t *testing.T) {
	storesuite.RunReceiptListEmpty(t, newTestStore(t).Bundle().Receipts)
}

func TestReceiptFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	storesuite.RunReceiptFKCascade(t, s.Bundle(), func(messageID string) {
		raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
		require.NoError(t, err)
		defer raw.Close()
		_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, messageID)
		require.NoError(t, err)
	})
}

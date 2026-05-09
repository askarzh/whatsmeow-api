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

func TestReactionPutGetList(t *testing.T) {
	storesuite.RunReactionPutGetList(t, newTestStore(t).Bundle())
}

func TestReactionPutIsUpsert(t *testing.T) {
	storesuite.RunReactionPutIsUpsert(t, newTestStore(t).Bundle())
}

func TestReactionDelete(t *testing.T) {
	storesuite.RunReactionDelete(t, newTestStore(t).Bundle())
}

func TestReactionListEmpty(t *testing.T) {
	storesuite.RunReactionListEmpty(t, newTestStore(t).Bundle().Reactions)
}

func TestReactionFKCascade(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	storesuite.RunReactionFKCascade(t, s.Bundle(), func(messageID string) {
		// Hard-delete the parent message via a sibling sql.DB (FK cascade requires foreign_keys=on).
		raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
		require.NoError(t, err)
		defer raw.Close()
		_, err = raw.Exec(`DELETE FROM messages WHERE id = ?`, messageID)
		require.NoError(t, err)
	})
}

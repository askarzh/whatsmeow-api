package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func TestNewCreatesAllTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Open a second handle to inspect sqlite_master without going through s.
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	expectedTables := []string{
		"chats", "contacts", "events_log", "kv", "media", "messages", "messages_fts", "reactions",
	}
	for _, table := range expectedTables {
		var name string
		err := raw.QueryRowContext(
			context.Background(),
			`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?`,
			table,
		).Scan(&name)
		assert.NoError(t, err, "table %q should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestNewIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s1, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	// Re-opening the same DB should succeed (migrations are no-op on second run).
	s2, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}

func TestBundleFieldsNonNil(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.New(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	b := s.Bundle()
	assert.NotNil(t, b.Chats)
	assert.NotNil(t, b.Messages)
	assert.NotNil(t, b.Contacts)
	assert.NotNil(t, b.Media)
	assert.NotNil(t, b.Events)
	assert.NotNil(t, b.KV)
	assert.NotNil(t, b.Reactions) // Plan 07b
}

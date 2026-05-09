package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/sqlite"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
	"github.com/stretchr/testify/require"
)

// newTestStore opens a fresh SQLite store in a temp dir. Used by all per-domain test files.
func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestChatPutGet(t *testing.T) {
	storesuite.RunChatPutGet(t, newTestStore(t).Bundle().Chats)
}

func TestChatGetNotFound(t *testing.T) {
	storesuite.RunChatGetNotFound(t, newTestStore(t).Bundle().Chats)
}

func TestChatPutIsUpsert(t *testing.T) {
	storesuite.RunChatPutIsUpsert(t, newTestStore(t).Bundle().Chats)
}

func TestChatList(t *testing.T) {
	storesuite.RunChatList(t, newTestStore(t).Bundle().Chats)
}

func TestChatCount(t *testing.T) {
	storesuite.RunChatCount(t, newTestStore(t).Bundle().Chats)
}

func TestChatTotalUnread(t *testing.T) {
	storesuite.RunChatTotalUnread(t, newTestStore(t).Bundle().Chats)
}

func TestChatSetArchived(t *testing.T) {
	storesuite.RunChatSetArchived(t, newTestStore(t).Bundle().Chats)
}

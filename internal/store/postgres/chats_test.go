package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestChatPutGet(t *testing.T) {
	storesuite.RunChatPutGet(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatGetNotFound(t *testing.T) {
	storesuite.RunChatGetNotFound(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatPutIsUpsert(t *testing.T) {
	storesuite.RunChatPutIsUpsert(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatList(t *testing.T) {
	storesuite.RunChatList(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatCount(t *testing.T) {
	storesuite.RunChatCount(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatTotalUnread(t *testing.T) {
	storesuite.RunChatTotalUnread(t, postgres.NewTestStore(t).Bundle().Chats)
}

func TestChatSetArchived(t *testing.T) {
	storesuite.RunChatSetArchived(t, postgres.NewTestStore(t).Bundle().Chats)
}

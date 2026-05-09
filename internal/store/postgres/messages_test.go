package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestMessagePutGet(t *testing.T) {
	storesuite.RunMessagePutGet(t, postgres.NewTestStore(t).Bundle())
}

func TestMessageGetNotFound(t *testing.T) {
	storesuite.RunMessageGetNotFound(t, postgres.NewTestStore(t).Bundle().Messages)
}

func TestMessagePutRequiresExistingChat(t *testing.T) {
	storesuite.RunMessagePutRequiresExistingChat(t, postgres.NewTestStore(t).Bundle().Messages)
}

func TestMessageListByChat(t *testing.T) {
	storesuite.RunMessageListByChat(t, postgres.NewTestStore(t).Bundle())
}

func TestMessageSearchFTS(t *testing.T) {
	storesuite.RunMessageSearchFTS(t, postgres.NewTestStore(t).Bundle())
}

func TestMessageSoftDelete(t *testing.T) {
	storesuite.RunMessageSoftDelete(t, postgres.NewTestStore(t).Bundle())
}

func TestMessagePutIsUpsert(t *testing.T) {
	storesuite.RunMessagePutIsUpsert(t, postgres.NewTestStore(t).Bundle())
}

func TestMessageCount(t *testing.T) {
	storesuite.RunMessageCount(t, postgres.NewTestStore(t).Bundle())
}

func TestMessageSearchExcludesSoftDeleted(t *testing.T) {
	storesuite.RunMessageSearchExcludesSoftDeleted(t, postgres.NewTestStore(t).Bundle())
}

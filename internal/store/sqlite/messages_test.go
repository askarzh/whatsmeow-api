package sqlite_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestMessagePutGet(t *testing.T) {
	storesuite.RunMessagePutGet(t, newTestStore(t).Bundle())
}

func TestMessageGetNotFound(t *testing.T) {
	storesuite.RunMessageGetNotFound(t, newTestStore(t).Bundle().Messages)
}

func TestMessagePutRequiresExistingChat(t *testing.T) {
	storesuite.RunMessagePutRequiresExistingChat(t, newTestStore(t).Bundle().Messages)
}

func TestMessageListByChat(t *testing.T) {
	storesuite.RunMessageListByChat(t, newTestStore(t).Bundle())
}

func TestMessageSearchFTS(t *testing.T) {
	storesuite.RunMessageSearchFTS(t, newTestStore(t).Bundle())
}

func TestMessageSoftDelete(t *testing.T) {
	storesuite.RunMessageSoftDelete(t, newTestStore(t).Bundle())
}

func TestMessagePutIsUpsert(t *testing.T) {
	storesuite.RunMessagePutIsUpsert(t, newTestStore(t).Bundle())
}

func TestMessageCount(t *testing.T) {
	storesuite.RunMessageCount(t, newTestStore(t).Bundle())
}

func TestMessageSearchExcludesSoftDeleted(t *testing.T) {
	storesuite.RunMessageSearchExcludesSoftDeleted(t, newTestStore(t).Bundle())
}

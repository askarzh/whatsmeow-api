package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestReceiptPutGetList(t *testing.T) {
	storesuite.RunReceiptPutGetList(t, postgres.NewTestStore(t).Bundle())
}

func TestReceiptPutIsUpsert(t *testing.T) {
	storesuite.RunReceiptPutIsUpsert(t, postgres.NewTestStore(t).Bundle())
}

func TestReceiptListEmpty(t *testing.T) {
	storesuite.RunReceiptListEmpty(t, postgres.NewTestStore(t).Bundle().Receipts)
}

func TestReceiptFKCascade(t *testing.T) {
	s := postgres.NewTestStore(t)
	storesuite.RunReceiptFKCascade(t, s.Bundle(), func(messageID string) {
		postgres.HardDeleteMessage(t, s, messageID)
	})
}

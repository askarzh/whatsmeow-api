package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestReactionPutGetList(t *testing.T) {
	storesuite.RunReactionPutGetList(t, postgres.NewTestStore(t).Bundle())
}

func TestReactionPutIsUpsert(t *testing.T) {
	storesuite.RunReactionPutIsUpsert(t, postgres.NewTestStore(t).Bundle())
}

func TestReactionDelete(t *testing.T) {
	storesuite.RunReactionDelete(t, postgres.NewTestStore(t).Bundle())
}

func TestReactionListEmpty(t *testing.T) {
	storesuite.RunReactionListEmpty(t, postgres.NewTestStore(t).Bundle().Reactions)
}

func TestReactionFKCascade(t *testing.T) {
	s := postgres.NewTestStore(t)
	storesuite.RunReactionFKCascade(t, s.Bundle(), func(messageID string) {
		postgres.HardDeleteMessage(t, s, messageID)
	})
}

package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestMediaPutGet(t *testing.T) {
	storesuite.RunMediaPutGet(t, postgres.NewTestStore(t).Bundle())
}

func TestMediaGetNotFound(t *testing.T) {
	storesuite.RunMediaGetNotFound(t, postgres.NewTestStore(t).Bundle().Media)
}

func TestMediaCascadesOnMessageDelete(t *testing.T) {
	s := postgres.NewTestStore(t)
	storesuite.RunMediaCascadesOnMessageDelete(t, s.Bundle(), func(messageID string) {
		postgres.HardDeleteMessage(t, s, messageID)
	})
}

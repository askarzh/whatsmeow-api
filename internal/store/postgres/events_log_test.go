package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestEventsAppendMonotonic(t *testing.T) {
	storesuite.RunEventsAppendMonotonic(t, postgres.NewTestStore(t).Bundle().Events)
}

func TestEventsSinceSeq(t *testing.T) {
	storesuite.RunEventsSinceSeq(t, postgres.NewTestStore(t).Bundle().Events)
}

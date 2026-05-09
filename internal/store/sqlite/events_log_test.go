package sqlite_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestEventsAppendMonotonic(t *testing.T) {
	storesuite.RunEventsAppendMonotonic(t, newTestStore(t).Bundle().Events)
}

func TestEventsSinceSeq(t *testing.T) {
	storesuite.RunEventsSinceSeq(t, newTestStore(t).Bundle().Events)
}

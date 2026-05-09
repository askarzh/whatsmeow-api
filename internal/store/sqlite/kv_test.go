package sqlite_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestKVSetGetDelete(t *testing.T) {
	storesuite.RunKVSetGetDelete(t, newTestStore(t).Bundle().KV)
}

func TestKVSetIsUpsert(t *testing.T) {
	storesuite.RunKVSetIsUpsert(t, newTestStore(t).Bundle().KV)
}

func TestKVDeleteAbsentIsNoop(t *testing.T) {
	storesuite.RunKVDeleteAbsentIsNoop(t, newTestStore(t).Bundle().KV)
}

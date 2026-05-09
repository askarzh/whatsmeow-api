package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestKVSetGetDelete(t *testing.T) {
	storesuite.RunKVSetGetDelete(t, postgres.NewTestStore(t).Bundle().KV)
}

func TestKVSetIsUpsert(t *testing.T) {
	storesuite.RunKVSetIsUpsert(t, postgres.NewTestStore(t).Bundle().KV)
}

func TestKVDeleteAbsentIsNoop(t *testing.T) {
	storesuite.RunKVDeleteAbsentIsNoop(t, postgres.NewTestStore(t).Bundle().KV)
}

package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/askarzh/whatsmeow-api/internal/store/storesuite"
)

func TestContactPutGet(t *testing.T) {
	storesuite.RunContactPutGet(t, postgres.NewTestStore(t).Bundle().Contacts)
}

func TestContactGetNotFound(t *testing.T) {
	storesuite.RunContactGetNotFound(t, postgres.NewTestStore(t).Bundle().Contacts)
}

func TestContactList(t *testing.T) {
	storesuite.RunContactList(t, postgres.NewTestStore(t).Bundle().Contacts)
}

func TestContactCount(t *testing.T) {
	storesuite.RunContactCount(t, postgres.NewTestStore(t).Bundle().Contacts)
}

func TestContactSearch(t *testing.T) {
	storesuite.RunContactSearch(t, postgres.NewTestStore(t).Bundle().Contacts)
}

func TestContactPutIsUpsert(t *testing.T) {
	storesuite.RunContactPutIsUpsert(t, postgres.NewTestStore(t).Bundle().Contacts)
}

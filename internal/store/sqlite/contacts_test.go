package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactPutGet(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	c := store.Contact{JID: "1@s.whatsapp.net", PushName: "Alice", FullName: "Alice A.", BusinessName: "ACME"}
	require.NoError(t, cs.Put(ctx, c))

	got, err := cs.Get(ctx, c.JID)
	require.NoError(t, err)
	assert.Equal(t, c, got)
}

func TestContactGetNotFound(t *testing.T) {
	_, err := newTestStore(t).Bundle().Contacts.Get(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestContactList(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "b@s.whatsapp.net", PushName: "B"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}))

	got, err := cs.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Ordered by jid ASC.
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)
	assert.Equal(t, "b@s.whatsapp.net", got[1].JID)
}

func TestContactCount(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts

	n, err := cs.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	require.NoError(t, cs.Put(ctx, store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "b@s.whatsapp.net", PushName: "B"}))
	n, err = cs.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestContactSearch(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts

	require.NoError(t, cs.Put(ctx, store.Contact{JID: "1@s.whatsapp.net", PushName: "Alice", FullName: "Alice Anderson"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "2@s.whatsapp.net", PushName: "Bob", BusinessName: "ACME Inc"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: "3@s.whatsapp.net", PushName: "carol"}))

	// push_name match (case-insensitive)
	got, err := cs.Search(ctx, "ALICE", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "1@s.whatsapp.net", got[0].JID)

	// full_name match
	got, err = cs.Search(ctx, "anderson", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "1@s.whatsapp.net", got[0].JID)

	// business_name match
	got, err = cs.Search(ctx, "acme", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2@s.whatsapp.net", got[0].JID)

	// substring match across multiple rows + limit
	got, err = cs.Search(ctx, "o", 2)
	require.NoError(t, err)
	require.LessOrEqual(t, len(got), 2)

	// no matches
	got, err = cs.Search(ctx, "zzzz", 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestContactPutIsUpsert(t *testing.T) {
	ctx := context.Background()
	cs := newTestStore(t).Bundle().Contacts
	jid := "x@s.whatsapp.net"
	require.NoError(t, cs.Put(ctx, store.Contact{JID: jid, PushName: "old"}))
	require.NoError(t, cs.Put(ctx, store.Contact{JID: jid, PushName: "new"}))
	got, err := cs.Get(ctx, jid)
	require.NoError(t, err)
	assert.Equal(t, "new", got.PushName)
}

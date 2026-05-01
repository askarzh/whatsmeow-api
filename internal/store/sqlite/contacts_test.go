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

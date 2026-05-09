package storesuite

import (
	"context"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/require"
)

// seedChat inserts a minimal chat row with the given jid. Several store-suite
// helpers need a chat row to exist before exercising messages / media /
// reactions / receipts (FK constraint).
func seedChat(t *testing.T, b store.Bundle, jid string) {
	t.Helper()
	require.NoError(t, b.Chats.Put(context.Background(), store.Chat{JID: jid, Name: jid, Kind: "user"}))
}

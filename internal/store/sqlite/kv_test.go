package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKVSetGetDelete(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV

	require.NoError(t, kv.Set(ctx, "k", "v"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)

	require.NoError(t, kv.Delete(ctx, "k"))
	_, err = kv.Get(ctx, "k")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestKVSetIsUpsert(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV
	require.NoError(t, kv.Set(ctx, "k", "old"))
	require.NoError(t, kv.Set(ctx, "k", "new"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func TestKVDeleteAbsentIsNoop(t *testing.T) {
	ctx := context.Background()
	kv := newTestStore(t).Bundle().KV
	assert.NoError(t, kv.Delete(ctx, "missing"))
}

package storesuite

import (
	"context"
	"errors"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func RunKVSetGetDelete(t *testing.T, kv store.KV) {
	ctx := context.Background()

	require.NoError(t, kv.Set(ctx, "k", "v"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)

	require.NoError(t, kv.Delete(ctx, "k"))
	_, err = kv.Get(ctx, "k")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func RunKVSetIsUpsert(t *testing.T, kv store.KV) {
	ctx := context.Background()
	require.NoError(t, kv.Set(ctx, "k", "old"))
	require.NoError(t, kv.Set(ctx, "k", "new"))
	got, err := kv.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func RunKVDeleteAbsentIsNoop(t *testing.T, kv store.KV) {
	ctx := context.Background()
	assert.NoError(t, kv.Delete(ctx, "missing"))
}

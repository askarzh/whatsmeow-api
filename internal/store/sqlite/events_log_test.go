package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsAppendMonotonic(t *testing.T) {
	ctx := context.Background()
	ev := newTestStore(t).Bundle().Events
	now := time.Now().UTC()

	s1, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":1}`})
	require.NoError(t, err)
	s2, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":2}`})
	require.NoError(t, err)
	s3, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "wa.msg", Payload: `{"a":3}`})
	require.NoError(t, err)

	assert.Equal(t, int64(1), s1)
	assert.Equal(t, int64(2), s2)
	assert.Equal(t, int64(3), s3)
}

func TestEventsSinceSeq(t *testing.T) {
	ctx := context.Background()
	ev := newTestStore(t).Bundle().Events
	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		_, err := ev.Append(ctx, store.EventLogEntry{Time: now, Type: "x", Payload: ""})
		require.NoError(t, err)
	}

	got, err := ev.SinceSeq(ctx, 2, 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, int64(3), got[0].Seq)
	assert.Equal(t, int64(5), got[2].Seq)

	// limit applies.
	got, err = ev.SinceSeq(ctx, 0, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].Seq)
	assert.Equal(t, int64(2), got[1].Seq)
}

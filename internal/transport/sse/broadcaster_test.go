package sse_test

import (
	"sync"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/stretchr/testify/assert"
)

func TestBroadcasterSingleSubscriber(t *testing.T) {
	b := sse.New(8)
	_, ch := b.Subscribe()

	b.Publish(sse.Event{Seq: 1, Kind: "message.received", Payload: []byte(`{"v":1}`)})

	select {
	case ev := <-ch:
		assert.Equal(t, int64(1), ev.Seq)
		assert.Equal(t, "message.received", ev.Kind)
		assert.JSONEq(t, `{"v":1}`, string(ev.Payload))
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestBroadcasterMultipleSubscribersFanOut(t *testing.T) {
	b := sse.New(8)
	_, ch1 := b.Subscribe()
	_, ch2 := b.Subscribe()

	b.Publish(sse.Event{Seq: 1, Kind: "x", Payload: []byte(`{}`)})

	for _, ch := range []<-chan sse.Event{ch1, ch2} {
		select {
		case ev := <-ch:
			assert.Equal(t, int64(1), ev.Seq)
		case <-time.After(time.Second):
			t.Fatal("subscriber missed event")
		}
	}
}

func TestBroadcasterUnsubscribeStopsDelivery(t *testing.T) {
	b := sse.New(8)
	id, ch := b.Subscribe()
	b.Unsubscribe(id)

	b.Publish(sse.Event{Seq: 1, Kind: "x", Payload: []byte(`{}`)})

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed after Unsubscribe")
	case <-time.After(100 * time.Millisecond):
		// closed channels return immediately; if we hit this branch, Unsubscribe
		// did not close the channel — also acceptable as long as no event was
		// delivered.
	}
}

func TestBroadcasterUnsubscribeIdempotent(t *testing.T) {
	b := sse.New(8)
	id, _ := b.Subscribe()
	b.Unsubscribe(id)
	assert.NotPanics(t, func() { b.Unsubscribe(id) })
}

func TestBroadcasterSlowSubscriberDropped(t *testing.T) {
	b := sse.New(2) // tiny buffer to force overflow
	_, ch := b.Subscribe()

	for i := 0; i < 10; i++ {
		b.Publish(sse.Event{Seq: int64(i + 1), Kind: "x", Payload: []byte(`{}`)})
	}

	// Drain; we expect the channel to close once the buffer overflows.
	closed := false
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
				goto done
			}
		case <-time.After(500 * time.Millisecond):
			goto done
		}
	}
done:
	assert.True(t, closed, "slow subscriber should have its channel closed on overflow")
}

func TestBroadcasterPublishConcurrent(t *testing.T) {
	b := sse.New(1024)
	_, ch := b.Subscribe()

	var wg sync.WaitGroup
	const N = 100
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(sse.Event{Seq: int64(i + 1), Kind: "x", Payload: []byte(`{}`)})
		}(i)
	}
	wg.Wait()

	got := 0
	timeout := time.After(time.Second)
	for got < N {
		select {
		case <-ch:
			got++
		case <-timeout:
			t.Fatalf("only got %d/%d events", got, N)
		}
	}
}

// Package sse provides an in-process pub/sub primitive used by the SSE event
// stream endpoint.
package sse

import "sync"

// Event is one item emitted on the stream. Seq matches the events_log row's
// Seq field; the SSE id: line uses this value.
type Event struct {
	Seq     int64
	Kind    string
	Payload []byte
}

// Broadcaster fans out Events to a set of subscriber channels. Each
// subscriber gets its own buffered channel; on overflow the broadcaster
// closes that channel (signaling "lagged") and removes it from the map.
type Broadcaster struct {
	mu      sync.RWMutex
	subs    map[uint64]chan Event
	next    uint64
	bufSize int
}

// New constructs a Broadcaster with the given per-subscriber buffer size.
// A buffer size of 0 is treated as 1.
func New(bufSize int) *Broadcaster {
	if bufSize < 1 {
		bufSize = 1
	}
	return &Broadcaster{
		subs:    make(map[uint64]chan Event),
		bufSize: bufSize,
	}
}

// Subscribe registers a new subscriber and returns its id (for Unsubscribe)
// and its receive channel. The channel is closed by the broadcaster on
// Unsubscribe or on overflow.
func (b *Broadcaster) Subscribe() (uint64, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := b.next
	ch := make(chan Event, b.bufSize)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes the subscriber and closes its channel. Idempotent.
func (b *Broadcaster) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Publish fans out an event to all current subscribers. If a subscriber's
// buffer is full it is dropped (channel closed, removed from the map).
func (b *Broadcaster) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			delete(b.subs, id)
			close(ch)
		}
	}
}

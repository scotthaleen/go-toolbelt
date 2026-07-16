package eventbus

import (
	"encoding/json"
	"io"
	"sync"
)

const defaultBufferSize = 32

// Publisher is a small injectable sink for in-process events.
type Publisher[T any] func(T)

// NoopPublisher returns a publisher that drops every event.
func NoopPublisher[T any]() Publisher[T] {
	return func(T) {}
}

// BrokerPublisher adapts a Broker to a Publisher.
func BrokerPublisher[T any](b *Broker[T]) Publisher[T] {
	if b == nil {
		return NoopPublisher[T]()
	}
	return b.Publish
}

// JSONPublisher writes each event as one JSON object per line.
func JSONPublisher[T any](w io.Writer) Publisher[T] {
	if w == nil {
		return NoopPublisher[T]()
	}
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	return func(evt T) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(evt)
	}
}

// Broker fan-outs events to in-process subscribers.
//
// Publishing is best-effort: slow subscribers drop events rather than blocking
// the publisher.
type Broker[T any] struct {
	mu          sync.RWMutex
	subscribers map[chan T]struct{}
}

func NewBroker[T any]() *Broker[T] {
	return &Broker[T]{subscribers: map[chan T]struct{}{}}
}

func (b *Broker[T]) Publish(evt T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Subscribe returns a channel of published events and an unsubscribe function.
// The unsubscribe function is safe to call more than once.
func (b *Broker[T]) Subscribe(size int) (<-chan T, func()) {
	if size <= 0 {
		size = defaultBufferSize
	}
	ch := make(chan T, size)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
}

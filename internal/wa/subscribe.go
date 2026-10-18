package wa

import (
	"fmt"
	"log/slog"
	"sync/atomic"
)

// Subscription delivers a Client's events on a buffered channel so they can
// be consumed from a single goroutine (a headless loop, or a tea.Cmd that
// receives the next event).
//
// It never blocks the protocol goroutines: when the buffer is full, events
// are dropped and counted. That is acceptable for display, but anything that
// must not lose messages (the message store) must not consume a lossy
// subscription.
type Subscription struct {
	C       <-chan Event
	dropped atomic.Uint64
	remove  func()
}

// Subscribe adds a handler to client that feeds a new Subscription with a
// buffer of size events. Call Close to remove the handler.
func Subscribe(client Client, size int, log *slog.Logger) *Subscription {
	ch := make(chan Event, size)
	s := &Subscription{C: ch}
	s.remove = client.AddEventHandler(func(evt Event) {
		select {
		case ch <- evt:
		default:
			s.dropped.Add(1)
			log.Warn("dropped event", "type", fmt.Sprintf("%T", evt))
		}
	})
	return s
}

// Dropped returns how many events have been discarded so far.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close stops delivery to the subscription. C is not closed, so a pending
// receive simply never completes; select on a context alongside it.
func (s *Subscription) Close() { s.remove() }

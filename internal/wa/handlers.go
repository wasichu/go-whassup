package wa

import "sync"

// Handlers is a concurrency-safe list of event handlers. Client
// implementations embed it to provide AddEventHandler.
type Handlers struct {
	mu     sync.RWMutex
	nextID int
	list   []handlerEntry
}

type handlerEntry struct {
	id int
	fn func(Event)
}

// AddEventHandler registers fn and returns a function that removes it.
func (h *Handlers) AddEventHandler(fn func(Event)) (remove func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	h.list = append(h.list, handlerEntry{id: id, fn: fn})
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, e := range h.list {
			if e.id == id {
				h.list = append(h.list[:i:i], h.list[i+1:]...)
				return
			}
		}
	}
}

// Emit calls every registered handler, in registration order, on the
// calling goroutine.
func (h *Handlers) Emit(evt Event) {
	h.mu.RLock()
	list := h.list
	h.mu.RUnlock()
	for _, e := range list {
		e.fn(evt)
	}
}

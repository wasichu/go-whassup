package wa

import (
	"slices"
	"testing"
)

func TestHandlersOrderAndRemove(t *testing.T) {
	var h Handlers
	var got []string
	removeA := h.AddEventHandler(func(Event) { got = append(got, "a") })
	h.AddEventHandler(func(Event) { got = append(got, "b") })

	h.Emit(Connected{})
	removeA()
	removeA() // idempotent
	h.Emit(Connected{})

	if want := []string{"a", "b", "b"}; !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestHandlersRemoveDuringEmitIsSafe(t *testing.T) {
	var h Handlers
	calls := 0
	var remove func()
	remove = h.AddEventHandler(func(Event) { calls++; remove() })
	h.AddEventHandler(func(Event) { calls++ })

	h.Emit(Connected{}) // both run: Emit iterates a snapshot
	h.Emit(Connected{}) // only the second remains
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

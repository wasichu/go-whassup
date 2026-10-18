package wa_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

func TestSubscribeDeliversAndCountsDrops(t *testing.T) {
	client := &watest.Client{}
	sub := wa.Subscribe(client, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))

	client.Emit(wa.Connected{})
	client.Emit(wa.Disconnected{})
	client.Emit(wa.Connected{}) // buffer full: dropped, must not block

	if got := sub.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d, want 1", got)
	}
	if got := <-sub.C; got != (wa.Connected{}) {
		t.Errorf("first event = %#v", got)
	}
	if got := <-sub.C; got != (wa.Disconnected{}) {
		t.Errorf("second event = %#v", got)
	}
}

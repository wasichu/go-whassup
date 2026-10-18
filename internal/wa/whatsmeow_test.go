package wa

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// newTestWhatsApp builds a WhatsApp with no live client, enough to exercise
// event translation.
func newTestWhatsApp(t *testing.T) (*WhatsApp, *[]Event) {
	t.Helper()
	w := &WhatsApp{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var got []Event
	w.AddEventHandler(func(e Event) { got = append(got, e) })
	return w, &got
}

func TestHandleEventMessage(t *testing.T) {
	w, got := newTestWhatsApp(t)
	ts := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

	w.handleEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:    types.NewADJID("15551234567", 0, 3),
				Sender:  types.NewADJID("15551234567", 0, 3),
				IsGroup: false,
			},
			ID:        "ABC",
			PushName:  "Ada",
			Timestamp: ts,
		},
		Message: &waE2E.Message{Conversation: proto.String("hello")},
	})

	want := Message{
		ID:        "ABC",
		Chat:      "15551234567@s.whatsapp.net",
		Sender:    "15551234567@s.whatsapp.net",
		PushName:  "Ada",
		Timestamp: ts,
		Kind:      KindText,
		Text:      "hello",
	}
	if len(*got) != 1 || (*got)[0] != want {
		t.Fatalf("events = %#v, want [%#v]", *got, want)
	}
}

func TestHandleEventSkipsProtocolMessages(t *testing.T) {
	w, got := newTestWhatsApp(t)
	w.handleEvent(&events.Message{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}})
	if len(*got) != 0 {
		t.Fatalf("expected no events, got %#v", *got)
	}
}

func TestHandleEventConnectionLifecycle(t *testing.T) {
	w, got := newTestWhatsApp(t)
	w.handleEvent(&events.PairSuccess{ID: types.NewADJID("15551234567", 0, 12)})
	w.handleEvent(&events.Connected{})
	w.handleEvent(&events.Disconnected{})
	w.handleEvent(&events.HistorySync{}) // empty history is not emitted

	want := []Event{
		PairSuccess{JID: "15551234567@s.whatsapp.net"},
		Connected{},
		Disconnected{},
	}
	if len(*got) != len(want) {
		t.Fatalf("events = %#v, want %#v", *got, want)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Errorf("event %d = %#v, want %#v", i, (*got)[i], want[i])
		}
	}
}

func TestEmitWithoutHandler(t *testing.T) {
	w := &WhatsApp{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w.handleEvent(&events.Connected{}) // must not panic
}

func TestOpenCreatesPrivateSessionDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	w, err := Open(t.Context(), path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session db mode = %o, want 600", perm)
	}
	if w.IsPaired() {
		t.Error("fresh store reports paired")
	}
	if err := w.Connect(t.Context()); !errors.Is(err, ErrNotPaired) {
		t.Errorf("Connect on fresh store = %v, want ErrNotPaired", err)
	}
}

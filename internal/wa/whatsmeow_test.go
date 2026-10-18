package wa

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
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
	if len(*got) != 1 || !reflect.DeepEqual((*got)[0], want) {
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
		if !reflect.DeepEqual((*got)[i], want[i]) {
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
	w, err := Open(t.Context(), path, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
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

func TestLogoutUnpaired(t *testing.T) {
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := w.Logout(t.Context()); !errors.Is(err, ErrNotPaired) {
		t.Errorf("Logout on fresh store = %v, want ErrNotPaired", err)
	}
}

func TestSendTextUnpaired(t *testing.T) {
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := w.SendText(t.Context(), "15551234567@s.whatsapp.net", "hi"); !errors.Is(err, ErrNotPaired) {
		t.Errorf("SendText unpaired = %v, want ErrNotPaired", err)
	}
}

func TestPairingOptionsAppliedAtPairTime(t *testing.T) {
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	w.CancelPairing() // nothing in progress: must be a no-op

	w.SetOptions(Options{RequestFullSync: true, FullSyncDays: 400})
	w.applyDeviceProps()
	if !store.DeviceProps.GetRequireFullSync() || store.DeviceProps.GetHistorySyncConfig().GetFullSyncDaysLimit() != 400 {
		t.Errorf("full sync props = %v / %d", store.DeviceProps.GetRequireFullSync(), store.DeviceProps.GetHistorySyncConfig().GetFullSyncDaysLimit())
	}
	if got := store.DeviceProps.GetOs(); got != deviceName {
		t.Errorf("Os = %q, want %q", got, deviceName)
	}

	w.SetOptions(Options{})
	w.applyDeviceProps()
	if store.DeviceProps.GetRequireFullSync() || store.DeviceProps.GetHistorySyncConfig().FullSyncDaysLimit != nil {
		t.Error("full sync props not reset")
	}
}

func TestMarkReadLocalSendsNothing(t *testing.T) {
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	msgs := []Message{{ID: "1", Chat: "15551234567@s.whatsapp.net"}}
	if err := w.MarkRead(t.Context(), "15551234567@s.whatsapp.net", msgs, ReadLocal); err != nil {
		t.Errorf("local mode should be a no-op, got %v", err)
	}
	if err := w.MarkRead(t.Context(), "15551234567@s.whatsapp.net", msgs, ReadDevices); !errors.Is(err, ErrNotPaired) {
		t.Errorf("unpaired devices mode = %v, want ErrNotPaired", err)
	}
}

func TestHandleEventChatRead(t *testing.T) {
	w, got := newTestWhatsApp(t)
	at := time.Unix(1700000000, 0)
	chat := types.NewJID("15551234567", types.DefaultUserServer)
	w.handleEvent(&events.Receipt{MessageSource: types.MessageSource{Chat: chat, IsFromMe: true}, Type: types.ReceiptTypeReadSelf, Timestamp: at})
	w.handleEvent(&events.Receipt{MessageSource: types.MessageSource{Chat: chat}, Type: types.ReceiptTypeRead, Timestamp: at}) // someone read OUR message: not a ChatRead
	want := ChatRead{Chat: chat.String(), At: at}
	if len(*got) != 1 || (*got)[0] != want {
		t.Errorf("events = %#v, want [%#v]", *got, want)
	}
}

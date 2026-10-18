package headless

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

// cancelWriter records output and cancels a context once marker is written,
// letting tests stop Follow deterministically.
type cancelWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	marker string
	cancel context.CancelFunc
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.marker != "" && strings.Contains(w.buf.String(), w.marker) {
		w.cancel()
	}
	return n, err
}

func (w *cancelWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func newConsole(in string, out io.Writer) Console {
	return Console{
		In:  strings.NewReader(in),
		Out: out,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestLoginSuccessPromptsForPhone(t *testing.T) {
	client := &watest.Client{
		PairCode:   "ABCD-EFGH",
		PairEvents: []wa.Event{wa.PairSuccess{JID: "15551234567@s.whatsapp.net"}, wa.Connected{}},
	}
	var out bytes.Buffer
	sub, err := newConsole("+1 555 123 4567\n", &out).Login(t.Context(), client, "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sub == nil {
		t.Fatal("Login returned nil subscription")
	}
	if got := client.PairPhone(); got != "+1 555 123 4567" {
		t.Errorf("paired phone = %q", got)
	}
	for _, want := range []string{"Pairing code: ABCD-EFGH", "Linked as 15551234567@s.whatsapp.net", "Connected"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestLoginAlreadyPaired(t *testing.T) {
	var out bytes.Buffer
	_, err := newConsole("", &out).Login(t.Context(), &watest.Client{Paired: true}, "")
	if !errors.Is(err, wa.ErrAlreadyPaired) {
		t.Fatalf("err = %v, want ErrAlreadyPaired", err)
	}
	if !strings.Contains(out.String(), "Already paired") {
		t.Errorf("output = %q", out.String())
	}
}

func TestLoginFailures(t *testing.T) {
	tests := []struct {
		name    string
		client  *watest.Client
		wantErr string
	}{
		{"pair failed", &watest.Client{PairEvents: []wa.Event{wa.PairFailed{Reason: "rejected"}}}, "pairing failed: rejected"},
		{"logged out", &watest.Client{PairEvents: []wa.Event{wa.LoggedOut{Reason: "unlinked"}}}, "logged out during pairing"},
		{"pair error", &watest.Client{PairErr: wa.ErrInvalidPhone}, "pairing:"},
		{"timeout", &watest.Client{}, "timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConsole("", io.Discard)
			c.PairTimeout = 10 * time.Millisecond
			_, err := c.Login(t.Context(), tt.client, "15551234567")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoginEmptyPrompt(t *testing.T) {
	_, err := newConsole("\n", io.Discard).Login(t.Context(), &watest.Client{}, "")
	if err == nil || !strings.Contains(err.Error(), "no input") {
		t.Fatalf("err = %v, want no input error", err)
	}
}

func TestListenNotPaired(t *testing.T) {
	err := newConsole("", io.Discard).Listen(t.Context(), &watest.Client{})
	if err == nil || !strings.Contains(err.Error(), "wa-tui login") {
		t.Fatalf("err = %v, want hint to run login", err)
	}
}

func TestListenPrintsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &cancelWriter{marker: "hello there", cancel: cancel}
	client := &watest.Client{
		Paired: true,
		ConnectEvents: []wa.Event{
			wa.Connected{},
			wa.Message{Chat: "1@s.whatsapp.net", PushName: "Ada", Text: "hello there", Timestamp: time.Now()},
		},
	}

	if err := newConsole("", out).Listen(ctx, client); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if !client.Connected() {
		t.Error("client was not connected")
	}
	for _, want := range []string{"-- wa.Connected", "Ada: hello there"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestFollowReportsDrops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &watest.Client{}
	sub := wa.Subscribe(client, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.Emit(wa.Connected{})
	client.Emit(wa.Disconnected{}) // dropped: buffer of 1 is full
	client.Emit(wa.Disconnected{}) // dropped

	out := &cancelWriter{marker: "dropped", cancel: cancel}
	if err := newConsole("", out).Follow(ctx, sub); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if !strings.Contains(out.String(), "dropped 2 events") {
		t.Errorf("output = %q", out.String())
	}
}

func TestSenderLabel(t *testing.T) {
	tests := []struct {
		msg  wa.Message
		want string
	}{
		{wa.Message{FromMe: true, PushName: "Me"}, "me"},
		{wa.Message{PushName: "Ada", Sender: "1@s"}, "Ada"},
		{wa.Message{Sender: "1@s"}, "1@s"},
	}
	for _, tt := range tests {
		if got := senderLabel(tt.msg); got != tt.want {
			t.Errorf("senderLabel(%+v) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

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
	if err == nil || !strings.Contains(err.Error(), "go-whassup login") {
		t.Fatalf("err = %v, want hint to run login", err)
	}
}

func TestListenPrintsEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &cancelWriter{marker: "history sync", cancel: cancel}
	client := &watest.Client{
		Paired: true,
		ConnectEvents: []wa.Event{
			wa.Connected{},
			wa.Message{Chat: "1@s.whatsapp.net", PushName: "Ada", Kind: wa.KindText, Text: "hello there", Timestamp: time.Now()},
			wa.HistorySync{Chats: []wa.Chat{{JID: "1@s.whatsapp.net"}}, Messages: make([]wa.Message, 3)},
		},
	}

	if err := newConsole("", out).Listen(ctx, client); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if !client.Connected() {
		t.Error("client was not connected")
	}
	for _, want := range []string{"-- wa.Connected", "Ada: hello there", "history sync: 1 chats, 3 messages"} {
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

func TestRelink(t *testing.T) {
	tests := []struct {
		name       string
		client     *watest.Client
		wantOut    string
		wantErr    string
		wantPaired bool
	}{
		{"not paired", &watest.Client{}, "pairing fresh", "", false},
		{"phone unlinked", &watest.Client{Paired: true, PhoneUnlinked: true}, "Removed go-whassup", "", false},
		{"local only", &watest.Client{Paired: true}, "Remove the old go-whassup entry", "", false},
		{"failure", &watest.Client{Paired: true, LogoutErr: errors.New("disk full")}, "", "unlinking: disk full", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := newConsole("", &out).Relink(t.Context(), tt.client)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Relink: %v", err)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output = %q, want containing %q", out.String(), tt.wantOut)
			}
			if tt.client.IsPaired() != tt.wantPaired {
				t.Errorf("paired after = %v, want %v", tt.client.IsPaired(), tt.wantPaired)
			}
		})
	}
}

func TestRelinkThenLogin(t *testing.T) {
	client := &watest.Client{
		Paired:        true,
		PhoneUnlinked: true,
		PairCode:      "ABCD-EFGH",
		PairEvents:    []wa.Event{wa.PairSuccess{JID: "1@s.whatsapp.net"}, wa.Connected{}},
	}
	var out bytes.Buffer
	c := newConsole("", &out)
	if err := c.Relink(t.Context(), client); err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if _, err := c.Login(t.Context(), client, "15551234567"); err != nil {
		t.Fatalf("Login after relink: %v", err)
	}
	if !strings.Contains(out.String(), "Pairing code: ABCD-EFGH") {
		t.Errorf("output:\n%s", out.String())
	}
}

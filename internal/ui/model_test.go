package ui

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

const (
	ada  = "15551234567@s.whatsapp.net"
	bob  = "15559876543@s.whatsapp.net"
	book = "120363000000000000@g.us"
)

var day = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	t       *testing.T
	m       tea.Model
	client  *watest.Client
	st      *store.Store
	pending []chan tea.Msg // blocked commands (e.g. waitEvent), as Bubble Tea keeps them
}

// newHarness builds the model wired like internal/app: recorder first,
// then the UI subscription. It seeds two chats.
func newHarness(t *testing.T, paired bool) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "messages.db"), nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := st.SaveMessages(t.Context(), []wa.Message{
		{ID: "a1", Chat: ada, Sender: ada, Timestamp: day, Kind: wa.KindText, Text: "hello from ada"},
		{ID: "b1", Chat: bob, Sender: bob, Timestamp: day.Add(-time.Hour), Kind: wa.KindText, Text: "hi from bob"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SaveChats(t.Context(), []wa.Chat{{JID: ada, Name: "Ada"}, {JID: bob, Name: "Bob"}}); err != nil {
		t.Fatalf("seed chats: %v", err)
	}

	client := &watest.Client{Paired: paired, ConnectEvents: []wa.Event{wa.Connected{}}}
	(&store.Recorder{Store: st, Log: log}).Attach(client)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	sub := wa.Subscribe(client, 64, log)
	t.Cleanup(sub.Close)

	h := &harness{t: t, client: client, st: st}
	h.m = New(ctx, Deps{Client: client, Store: st, Log: log}, sub)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.run(h.m.Init())
	h.settle()
	return h
}

// send delivers msg and runs the resulting commands.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(msg)
	h.run(cmd)
}

// run executes cmd and feeds results back, recursively. A command that
// blocks (the pending waitEvent) is parked; settle delivers it later.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		h.deliver(msg)
	case <-time.After(50 * time.Millisecond):
		h.pending = append(h.pending, done)
	}
}

// settle delivers results from parked commands that have since completed,
// until nothing more happens.
func (h *harness) settle() {
	h.t.Helper()
	for progress := true; progress; {
		progress = false
		parked := h.pending
		h.pending = nil
		for _, ch := range parked {
			select {
			case msg := <-ch:
				h.deliver(msg)
				progress = true
			case <-time.After(50 * time.Millisecond):
				h.pending = append(h.pending, ch)
			}
		}
	}
}

func (h *harness) deliver(msg tea.Msg) {
	h.t.Helper()
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.send(msg)
	}
}

func (h *harness) key(k string) {
	h.t.Helper()
	switch k {
	case "enter":
		h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	case "esc":
		h.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	case "down":
		h.send(tea.KeyPressMsg{Code: tea.KeyDown})
	case "ctrl+c":
		h.send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	default:
		for _, r := range k {
			h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

func (h *harness) view() string { return h.m.View().Content }

func (h *harness) model() Model { return h.m.(Model) }

func TestInitShowsChatsAndFirstConversation(t *testing.T) {
	h := newHarness(t, true)
	v := h.view()
	for _, want := range []string{"Chats", "Ada", "Bob", "hello from ada", "● online"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestSelectingChatLoadsItsMessages(t *testing.T) {
	h := newHarness(t, true)
	h.key("down")
	if got := h.model().conv.chat.JID; got != bob {
		t.Fatalf("selected = %q, want bob", got)
	}
	if !strings.Contains(h.view(), "hi from bob") {
		t.Errorf("bob's messages not shown:\n%s", h.view())
	}
}

func TestTypingQInInputDoesNotQuit(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter") // focus input
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("typing q in the input quit the app")
		}
	}
	if got := h.model().conv.input.Value(); got != "q" {
		t.Errorf("input = %q, want q", got)
	}
}

func TestQuitKeys(t *testing.T) {
	h := newHarness(t, true)
	for _, key := range []tea.KeyPressMsg{{Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}} {
		_, cmd := h.m.Update(key)
		if cmd == nil {
			t.Fatalf("%s: no command", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: not a quit", key)
		}
	}
}

func TestSendShowsMessageViaEventPath(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter")
	h.key("see you soon")
	h.key("enter")

	if sent := h.client.SentMessages(); len(sent) != 1 || sent[0].Chat != ada || sent[0].Text != "see you soon" {
		t.Fatalf("sent = %+v", sent)
	}
	if got := h.model().conv.input.Value(); got != "" {
		t.Errorf("input not cleared: %q", got)
	}
	// The fake emitted the message; let the pending waitEvent deliver it.
	h.settle()
	if !strings.Contains(h.view(), "see you soon") {
		t.Errorf("sent message not shown:\n%s", h.view())
	}
	stored, err := h.st.Messages(t.Context(), ada, 0)
	if err != nil || len(stored) != 2 {
		t.Errorf("stored = %v, %v; want the sent message recorded", stored, err)
	}
}

func TestSendErrorShownInStatus(t *testing.T) {
	h := newHarness(t, true)
	h.client.SendErr = errors.New("websocket not connected")
	h.key("enter")
	h.key("hi")
	h.key("enter")
	if !strings.Contains(h.view(), "sending: websocket not connected") {
		t.Errorf("error not shown:\n%s", h.view())
	}
}

func TestIncomingMessageRefreshesOpenChat(t *testing.T) {
	h := newHarness(t, true)
	h.client.Emit(wa.Message{ID: "a2", Chat: ada, Sender: ada, Timestamp: day.Add(time.Minute), Kind: wa.KindText, Text: "are you there?"})
	h.settle()
	if !strings.Contains(h.view(), "are you there?") {
		t.Errorf("incoming message not shown:\n%s", h.view())
	}
}

func TestConnectionStates(t *testing.T) {
	h := newHarness(t, false)
	if !strings.Contains(h.view(), "not paired") {
		t.Errorf("unpaired status missing:\n%s", h.view())
	}
	h.send(eventMsg{evt: wa.LoggedOut{Reason: "unlinked"}})
	if !strings.Contains(h.view(), "login -relink") {
		t.Errorf("logged-out hint missing:\n%s", h.view())
	}
	h.send(eventMsg{evt: wa.Disconnected{}})
	if !strings.Contains(h.view(), "offline") {
		t.Errorf("offline status missing:\n%s", h.view())
	}
}

func TestTooSmall(t *testing.T) {
	h := newHarness(t, true)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	if !strings.Contains(h.view(), "Terminal too small") {
		t.Errorf("view = %q", h.view())
	}
}

func TestRenderMessagesDaySeparatorsAndNames(t *testing.T) {
	chat := store.ChatSummary{JID: book, Name: "Book club", IsGroup: true}
	out := renderMessages([]wa.Message{
		{Chat: book, Sender: ada, PushName: "Ada", IsGroup: true, Timestamp: day, Kind: wa.KindText, Text: "ch 3?"},
		{Chat: book, Sender: bob, IsGroup: true, Timestamp: day.Add(24 * time.Hour), Kind: wa.KindImage},
		{Chat: book, FromMe: true, IsGroup: true, Timestamp: day.Add(25 * time.Hour), Kind: wa.KindText, Text: "yes"},
	}, chat, 80)
	for _, want := range []string{"Ada", "ch 3?", "15559876543", "[image]", "me", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "──"); n < 4 { // two day separators, each with two rules
		t.Errorf("expected two day separators:\n%s", out)
	}
}

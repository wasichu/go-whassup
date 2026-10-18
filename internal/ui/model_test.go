package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/wasichu/go-whassup/internal/config"
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
	applied []config.Config // configs passed to ApplyConfig
	cfgPath string
	t       *testing.T
	m       tea.Model
	client  *watest.Client
	st      *store.Store
	pending []chan tea.Msg // blocked commands (e.g. waitEvent), as Bubble Tea keeps them
	raw     []string       // escape sequences sent with tea.Raw
}

// withGraphics simulates a true-color terminal with Kitty graphics support.
func (h *harness) withGraphics() {
	m := h.model()
	m.deps.TerminalGraphics = true
	h.m = m
	h.send(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
}

func (h *harness) rawContains(s string) bool {
	for _, r := range h.raw {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
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

	h := &harness{t: t, client: client, st: st, cfgPath: filepath.Join(t.TempDir(), "config.toml")}
	h.m = New(ctx, Deps{
		Client: client, Store: st, Log: log,
		Config: config.Default(), ConfigPath: h.cfgPath,
		ApplyConfig: func(_ context.Context, c config.Config) error {
			h.applied = append(h.applied, c)
			return nil
		},
	}, sub)
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
	case tea.RawMsg:
		h.raw = append(h.raw, fmt.Sprint(msg.Msg))
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
	case "up":
		h.send(tea.KeyPressMsg{Code: tea.KeyUp})
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
	h := newHarness(t, true)
	h.send(eventMsg{evt: wa.Disconnected{}})
	if !strings.Contains(h.view(), "offline") {
		t.Errorf("offline status missing:\n%s", h.view())
	}
	h.send(eventMsg{evt: wa.LoggedOut{Reason: "unlinked"}})
	if h.model().screen != screenLogin || !strings.Contains(h.view(), "Logged out by WhatsApp (unlinked)") {
		t.Errorf("logged out should open the login screen:\n%s", h.view())
	}
}

func TestUnpairedStartsOnLoginScreen(t *testing.T) {
	h := newHarness(t, false)
	v := h.view()
	for _, want := range []string{"Link go-whassup to WhatsApp", "phone number code", "QR code", "not linked"} {
		if !strings.Contains(v, want) {
			t.Errorf("login view missing %q:\n%s", want, v)
		}
	}
}

func TestPhonePairingFlow(t *testing.T) {
	h := newHarness(t, false)
	h.client.PairCode = "ABCD-EFGH"
	h.key("enter") // phone code option
	h.key("+1 555 123 4567")
	h.key("enter")
	if got := h.client.PairPhone(); got != "+1 555 123 4567" {
		t.Fatalf("paired phone = %q", got)
	}
	if !strings.Contains(h.view(), "A B C D - E F G H") {
		t.Fatalf("code not shown:\n%s", h.view())
	}

	h.client.Emit(wa.PairSuccess{JID: ada})
	h.settle()
	if !strings.Contains(h.view(), "Linked as "+ada) {
		t.Errorf("linked state missing:\n%s", h.view())
	}
	h.client.Emit(wa.Connected{})
	h.settle()
	if h.model().screen != screenMain || !strings.Contains(h.view(), "● online") {
		t.Errorf("should return to main, online:\n%s", h.view())
	}
}

func TestPhonePairingRejectsBadNumber(t *testing.T) {
	h := newHarness(t, false)
	h.key("enter")
	h.key("0555")
	h.key("enter")
	if h.client.PairPhone() != "" {
		t.Error("Pair called with an invalid number")
	}
	if !strings.Contains(h.view(), "international format") {
		t.Errorf("validation error missing:\n%s", h.view())
	}
}

func TestQRPairingAndCancel(t *testing.T) {
	h := newHarness(t, false)
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 45})
	h.key("down")
	h.key("enter") // QR option
	h.settle()
	if !strings.Contains(h.view(), "▀") || !strings.Contains(h.view(), "scan") {
		t.Fatalf("QR not shown:\n%s", h.view())
	}

	h.key("esc") // cancels pairing, stays on the screen
	if h.client.Cancelled() != 1 || h.model().screen != screenLogin || h.model().login.step != loginChoose {
		t.Errorf("first esc: cancelled=%d screen=%v step=%v", h.client.Cancelled(), h.model().screen, h.model().login.step)
	}
	// A late QR from the cancelled pairing must not reopen the QR view.
	h.client.Emit(wa.PairQRCode{Code: sampleQR})
	h.settle()
	if h.model().login.step != loginChoose {
		t.Error("late QR event changed the step")
	}
}

func TestQRLayoutFitsCommonWindows(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantFit       bool
		wantShort     string
	}{
		{"wide but short (the reported 137x37)", 137, 37, true, ""},
		{"narrow, just tall enough", 80, 37, true, ""},
		{"standard 80x24", 80, 24, false, "more rows"},
		{"too narrow", 60, 50, false, "more columns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, false)
			h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
			h.send(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			h.key("down")
			h.key("enter")
			h.settle()
			v := h.view()
			if got := strings.Contains(v, "▀"); got != tt.wantFit {
				t.Fatalf("QR shown = %v, want %v:\n%s", got, tt.wantFit, v)
			}
			if tt.wantShort != "" && !strings.Contains(v, tt.wantShort) {
				t.Errorf("hint should mention %q:\n%s", tt.wantShort, v)
			}
			if lines := strings.Count(v, "\n") + 1; lines > tt.height {
				t.Errorf("view is %d lines in a %d-line terminal", lines, tt.height)
			}
		})
	}
}

func TestQRAppearsAfterResize(t *testing.T) {
	h := newHarness(t, false)
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.key("down")
	h.key("enter")
	h.settle()
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40}) // user zooms out
	if !strings.Contains(h.view(), "▀") {
		t.Errorf("QR should appear once it fits:\n%s", h.view())
	}
}

func TestPairFailedAndRetry(t *testing.T) {
	h := newHarness(t, false)
	h.client.PairCode = "ABCD-EFGH"
	h.key("enter")
	h.key("15551234567")
	h.key("enter")
	h.client.Emit(wa.PairFailed{Reason: "timed out"})
	h.settle()
	if !strings.Contains(h.view(), "Pairing failed: timed out") {
		t.Fatalf("failure not shown:\n%s", h.view())
	}
	h.key("enter")
	if h.model().login.step != loginChoose {
		t.Errorf("enter after failure should return to choose, step = %v", h.model().login.step)
	}
}

func TestRelinkFromMainScreen(t *testing.T) {
	h := newHarness(t, true)
	h.client.PhoneUnlinked = true
	h.key("L")
	if h.model().screen != screenLogin || !strings.Contains(h.view(), "Re-link this device") {
		t.Fatalf("L should open login with re-link:\n%s", h.view())
	}
	h.key("enter")
	if !strings.Contains(h.view(), "Re-link now? (y/n)") {
		t.Fatalf("confirmation missing:\n%s", h.view())
	}
	h.key("y")
	if h.client.IsPaired() {
		t.Fatal("Logout not called")
	}
	v := h.view()
	if !strings.Contains(v, "Unlinked. Your stored messages are kept.") || !strings.Contains(v, "Link with a QR code") {
		t.Errorf("after unlink should offer pairing:\n%s", v)
	}
	h.key("esc") // leave without pairing
	if h.model().screen != screenMain {
		t.Error("esc from choose should return to main")
	}
}

func TestShortcutsAreTypedInInput(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter")
	h.key("L,?q")
	m := h.model()
	if m.screen != screenMain || m.showHelp || m.conv.input.Value() != "L,?q" {
		t.Errorf("screen=%v help=%v input=%q", m.screen, m.showHelp, m.conv.input.Value())
	}
}

func TestHelpOverlay(t *testing.T) {
	h := newHarness(t, true)
	h.key("?")
	v := h.view()
	for _, want := range []string{"Keys", "link/re-link", "send", "scroll up"} {
		if !strings.Contains(v, want) {
			t.Errorf("help missing %q:\n%s", want, v)
		}
	}
	h.key("x")
	if h.model().showHelp {
		t.Error("any key should close help")
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

func TestScrollPositionSurvivesOtherChatActivity(t *testing.T) {
	h := newHarness(t, true)
	var msgs []wa.Message
	for i := range 60 { // enough to scroll
		msgs = append(msgs, wa.Message{ID: fmt.Sprintf("x%d", i), Chat: ada, Sender: ada, Timestamp: day.Add(time.Duration(i) * time.Minute), Kind: wa.KindText, Text: fmt.Sprintf("line %d", i)})
	}
	if _, err := h.st.SaveMessages(t.Context(), msgs); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.client.Emit(wa.HistorySync{}) // trigger a reload of the open chat
	h.settle()
	h.send(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m := h.model()
	before := m.conv.viewport.YOffset()
	if m.conv.viewport.AtBottom() {
		t.Fatal("setup: expected to be scrolled up")
	}

	h.client.Emit(wa.Message{ID: "b2", Chat: bob, Sender: bob, Timestamp: day.Add(2 * time.Hour), Kind: wa.KindText, Text: "elsewhere"})
	h.settle()
	m = h.model()
	if after := m.conv.viewport.YOffset(); after != before {
		t.Errorf("scroll moved from %d to %d after activity in another chat", before, after)
	}
}

func TestSettingsToggleEditValidateSave(t *testing.T) {
	h := newHarness(t, true)
	h.key(",")
	v := h.view()
	for _, want := range []string{"Settings", "[history]", "request_full_sync", "keep_days", "Applies the next time you link"} {
		if !strings.Contains(v, want) {
			t.Errorf("settings view missing %q:\n%s", want, v)
		}
	}

	// full_sync_days without request_full_sync is invalid: kept, error shown.
	h.key("down")
	h.key("enter")
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("30")
	h.key("enter")
	if !strings.Contains(h.view(), "only used with") || len(h.applied) != 0 {
		t.Fatalf("cross-field validation not shown or config applied:\n%s", h.view())
	}
	h.key("esc") // cancel the edit

	// Toggle request_full_sync with space: saved and applied.
	h.key("up")
	h.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if len(h.applied) != 1 || !h.applied[0].History.RequestFullSync {
		t.Fatalf("toggle not applied: %+v\n%s", h.applied, h.view())
	}
	saved, err := config.Load(h.cfgPath)
	if err != nil || !saved.History.RequestFullSync {
		t.Fatalf("toggle not saved: %+v, %v", saved, err)
	}
	if !strings.Contains(h.view(), "Saved to") {
		t.Errorf("save confirmation missing:\n%s", h.view())
	}

	// Edit keep_days: junk rejected, then a valid value saved.
	h.key("down")
	h.key("down")
	h.key("enter")
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("x")
	h.key("enter")
	if !strings.Contains(h.view(), "whole number") {
		t.Errorf("parse error missing:\n%s", h.view())
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("90")
	h.key("enter")
	if saved, err := config.Load(h.cfgPath); err != nil || saved.History.KeepDays != 90 {
		t.Errorf("keep_days not saved: %+v, %v", saved, err)
	}
	if got := h.model().deps.Config.History.KeepDays; got != 90 {
		t.Errorf("model config not updated: %d", got)
	}

	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should return to main")
	}
	h.key(",")
	if h.model().settings.cfg.History.KeepDays != 90 {
		t.Error("reopened settings lost the saved value")
	}
}

func TestInlineQRImage(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24}) // too small for the text QR
	h.key("down")
	h.key("enter")
	h.settle()

	v := h.view()
	if !strings.Contains(v, string(rune(0x10EEEE))) || strings.Contains(v, "doesn't fit") {
		t.Fatalf("inline image placeholders should show in 80x24:\n%s", v)
	}
	if !h.rawContains("a=T,U=1") {
		t.Fatal("image was not transmitted")
	}

	h.key("esc") // cancel: image freed
	if !h.rawContains("a=d,d=I") || h.model().login.image {
		t.Error("image not deleted on cancel")
	}
}

func TestInlineImagesRespectSetting(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	cfg := h.model().deps.Config
	cfg.UI.InlineImages = false
	m := h.model()
	m.deps.Config = cfg
	h.m = m
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if strings.Contains(h.view(), string(rune(0x10EEEE))) || h.rawContains("a=T") {
		t.Error("inline_images = false should draw text blocks")
	}
	if !strings.Contains(h.view(), "▀") {
		t.Error("text-block QR missing")
	}
}

func TestInlineImagesNeedTrueColor(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	h.send(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if h.rawContains("a=T") || !strings.Contains(h.view(), "▀") {
		t.Error("without true color, the QR should fall back to text blocks")
	}
}

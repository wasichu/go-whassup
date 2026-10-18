package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
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
	cache   media.Cache
	opened  []string // files passed to OpenFile
	openErr error
}

// withGraphics simulates a true-color terminal with Kitty graphics support.
func (h *harness) withGraphics() {
	m := h.model()
	m.deps.Graphics = termcaps.Graphics{Kitty: true}
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

	h := &harness{t: t, client: client, st: st, cfgPath: filepath.Join(t.TempDir(), "config.toml"),
		cache: media.Cache{Dir: filepath.Join(t.TempDir(), "media")}}
	h.m = New(ctx, Deps{
		Client: client, Store: st, Log: log,
		Config: config.Default(), ConfigPath: h.cfgPath,
		Media: h.cache,
		OpenFile: func(path string) error {
			h.opened = append(h.opened, path)
			return h.openErr
		},
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
	case <-time.After(20 * time.Millisecond):
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
			case <-time.After(20 * time.Millisecond):
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
	}, chat, 80, renderOpts{})
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

func TestTmuxHintAndWrapping(t *testing.T) {
	h := newHarness(t, false)
	m := h.model()
	m.deps.Graphics = termcaps.Graphics{Hint: "enable tmux passthrough"}
	h.m = m
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if !strings.Contains(h.view(), "Tip: enable tmux passthrough") || !strings.Contains(h.view(), "▀") {
		t.Errorf("hint with text QR expected:\n%s", h.view())
	}

	h2 := newHarness(t, false)
	h2.withGraphics()
	m2 := h2.model()
	m2.deps.Graphics.Tmux = true
	h2.m = m2
	h2.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h2.key("down")
	h2.key("enter")
	h2.settle()
	if !h2.rawContains("\x1bPtmux;") {
		t.Error("upload not wrapped for tmux")
	}
}

const carol = "15550000002@s.whatsapp.net"

func (h *harness) addContact(c wa.Contact) {
	h.t.Helper()
	if err := h.st.SaveContacts(h.t.Context(), []wa.Contact{c}); err != nil {
		h.t.Fatalf("SaveContacts: %v", err)
	}
}

func TestFinderOpensExistingChat(t *testing.T) {
	h := newHarness(t, true)
	h.key("/")
	if h.model().screen != screenFinder || !strings.Contains(h.view(), "Find a chat or contact") {
		t.Fatalf("finder not open:\n%s", h.view())
	}
	h.key("bob")
	if !strings.Contains(h.view(), "› Bob") {
		t.Fatalf("bob not the top match:\n%s", h.view())
	}
	h.key("enter")
	m := h.model()
	if m.screen != screenMain || m.conv.chat.JID != bob || m.chats.selectedJID() != bob || m.focus != focusInput {
		t.Errorf("screen=%v chat=%q selected=%q focus=%v", m.screen, m.conv.chat.JID, m.chats.selectedJID(), m.focus)
	}
	if !strings.Contains(h.view(), "hi from bob") {
		t.Errorf("bob's messages not loaded:\n%s", h.view())
	}
}

func TestFinderStartsNewChat(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: carol, Name: "Carol"})
	h.key("/")
	h.key("car")
	if !strings.Contains(h.view(), "new chat · +15550000002") {
		t.Fatalf("contact without chat should be marked new:\n%s", h.view())
	}
	h.key("enter")
	m := h.model()
	if m.draft != carol || m.conv.chat.JID != carol || !strings.Contains(h.view(), "Say hi!") {
		t.Fatalf("draft not opened: draft=%q\n%s", m.draft, h.view())
	}

	h.key("hello carol")
	h.key("enter")
	h.settle()
	m = h.model()
	if m.draft != "" || m.chats.selectedJID() != carol {
		t.Errorf("after first message the draft should be a real, selected chat: draft=%q selected=%q", m.draft, m.chats.selectedJID())
	}
	if !strings.Contains(h.view(), "hello carol") || !strings.Contains(h.view(), " Carol") {
		t.Errorf("new chat not shown:\n%s", h.view())
	}
}

func TestFinderEscAndTypedSlash(t *testing.T) {
	h := newHarness(t, true)
	h.key("/")
	h.key("jk") // letters in the query, not navigation
	if got := h.model().finder.input.Value(); got != "jk" {
		t.Errorf("query = %q", got)
	}
	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should close the finder")
	}
	h.key("enter")
	h.key("a/b")
	if h.model().screen != screenMain || h.model().conv.input.Value() != "a/b" {
		t.Error("/ in the message box must be typed")
	}
}

func TestContactsEventRefreshesNames(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: ada, Name: "Ada Lovelace"})
	h.client.Emit(wa.Contacts{List: []wa.Contact{{JID: ada, Name: "Ada Lovelace"}}})
	h.settle()
	if !strings.Contains(h.view(), "Ada Lovelace") {
		t.Errorf("chat list should show the address-book name:\n%s", h.view())
	}
}

func TestOwnChatLabelledYou(t *testing.T) {
	own := "12025550100@s.whatsapp.net"
	h := newHarness(t, true)
	h.client.Own = own
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "s1", Chat: own, Sender: own, FromMe: true, Timestamp: day.Add(2 * time.Hour), Kind: wa.KindText, Text: "note to self"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.client.Emit(wa.Connected{}) // the UI re-reads its own JID on connect
	h.settle()
	v := h.view()
	if !strings.Contains(v, "+12025550100 (You)") {
		t.Errorf("own chat should read \"+12025550100 (You)\":\n%s", v)
	}
	if strings.Contains(v, "@s.whatsapp.net (You)") {
		t.Error("raw JID shown")
	}
}

// seedUnread adds n incoming messages to chat after everything else.
func (h *harness) seedUnread(chat string, n int) {
	h.t.Helper()
	var msgs []wa.Message
	for i := range n {
		msgs = append(msgs, wa.Message{ID: fmt.Sprintf("u%s%d", chat[:3], i), Chat: chat, Sender: chat, Timestamp: day.Add(3*time.Hour + time.Duration(i)*time.Minute), Kind: wa.KindText, Text: fmt.Sprintf("unread %d", i)})
	}
	if _, err := h.st.SaveMessages(h.t.Context(), msgs); err != nil {
		h.t.Fatalf("seed: %v", err)
	}
	if err := h.st.MarkRead(h.t.Context(), chat, day.Add(2*time.Hour)); err != nil {
		h.t.Fatalf("seed read: %v", err)
	}
}

func (h *harness) unread(chat string) int {
	h.t.Helper()
	chats, err := h.st.Chats(h.t.Context())
	if err != nil {
		h.t.Fatalf("Chats: %v", err)
	}
	for _, c := range chats {
		if c.JID == chat {
			return c.Unread
		}
	}
	return -1
}

func newHarnessWithUnread(t *testing.T, mode string) *harness {
	h := newHarness(t, true)
	h.seedUnread(bob, 2)
	m := h.model()
	m.deps.Config.Privacy.MarkRead = mode
	m.termFocused = true
	h.m = m
	h.client.Emit(wa.HistorySync{}) // reload so the badge appears
	h.settle()
	h.selectChat(bob)
	return h
}

// selectChat moves the list selection onto jid with arrow keys.
func (h *harness) selectChat(jid string) {
	h.t.Helper()
	for range 10 {
		m := h.model()
		if m.chats.selectedJID() == jid {
			return
		}
		h.key("up")
	}
	h.t.Fatalf("could not select %s", jid)
}

func TestUnreadBadgeAndDivider(t *testing.T) {
	h := newHarnessWithUnread(t, config.MarkReadLocalOnly)
	v := h.view()
	if !strings.Contains(v, "── 2 new ──") {
		t.Errorf("new-messages divider missing:\n%s", v)
	}
	// After the dwell the chat is read: badge gone, divider stays while open.
	h.settle()
	if got := h.unread(bob); got != 0 {
		t.Errorf("bob unread after dwell = %d, want 0", got)
	}
	if !strings.Contains(h.view(), "── 2 new ──") {
		t.Error("divider should stay while the chat is open")
	}
	if calls := h.client.ReadCalls(); len(calls) != 0 {
		t.Errorf("local mode must send nothing, sent %+v", calls)
	}
}

func TestBadgeShowsCountForOtherChats(t *testing.T) {
	h := newHarness(t, true)
	h.seedUnread(ada, 3)
	h.key("down") // move the selection to bob so ada isn't being read
	h.client.Emit(wa.HistorySync{})
	h.settle()
	if !strings.Contains(h.view(), " 3 ") {
		t.Errorf("unread badge for ada missing:\n%s", h.view())
	}
}

func TestMarkReadModesSend(t *testing.T) {
	for setting, mode := range map[string]wa.ReadMode{
		config.MarkReadReceiptsOff: wa.ReadDevices,
		config.MarkReadReceiptsOn:  wa.ReadEveryone,
	} {
		t.Run(setting, func(t *testing.T) {
			h := newHarnessWithUnread(t, setting)
			h.settle()
			calls := h.client.ReadCalls()
			if len(calls) != 1 || calls[0].Chat != bob || calls[0].Mode != mode || len(calls[0].IDs) != 2 {
				t.Errorf("read calls = %+v", calls)
			}
		})
	}
}

func TestNoReadWhileUnfocused(t *testing.T) {
	h := newHarness(t, true)
	h.send(tea.BlurMsg{})
	h.seedUnread(bob, 1)
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(bob)
	h.settle()
	if got := h.unread(bob); got != 1 {
		t.Fatalf("read while the terminal was unfocused: unread = %d", got)
	}
	h.send(tea.FocusMsg{})
	h.settle()
	if got := h.unread(bob); got != 0 {
		t.Errorf("focusing should read the open chat, unread = %d", got)
	}
}

func TestArrowingPastChatsDoesNotReadThem(t *testing.T) {
	h := newHarness(t, true)
	h.seedUnread(ada, 1)
	h.seedUnread(bob, 1)
	h.client.Emit(wa.HistorySync{})
	h.settle() // the first-selected chat is read after its dwell
	m := h.model()
	first := m.chats.selectedJID()
	other := ada
	if first == ada {
		other = bob
	}
	// Two quick moves land back on the first chat: the other one was only
	// passed over, never dwelled on.
	h.m, _ = h.m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	h.m, _ = h.m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	h.settle()
	if got := h.unread(other); got != 1 {
		t.Errorf("chat passed over was marked read: unread = %d", got)
	}
}

func TestOpeningLongUnreadChatStartsAtDivider(t *testing.T) {
	h := newHarness(t, true)
	h.send(tea.BlurMsg{}) // keep it unread while we look
	h.send(tea.WindowSizeMsg{Width: 90, Height: 16})
	h.seedUnread(bob, 20)
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(bob)
	v := h.view()
	if !strings.Contains(v, "── 20 new ──") || strings.Contains(v, "unread 19") {
		t.Errorf("should open at the divider, not the bottom:\n%s", v)
	}
}

func TestLaunchAloneReadsNothing(t *testing.T) {
	h := newHarness(t, true)
	h.seedUnread(ada, 1)
	h.client.Emit(wa.HistorySync{})
	h.settle() // ada is open on screen, but no key has been pressed
	if got := h.unread(ada); got != 1 {
		t.Errorf("launching marked the open chat read: unread = %d", got)
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyPgDown}) // any key counts as being here
	h.client.Emit(wa.HistorySync{})
	h.settle()
	if got := h.unread(ada); got != 0 {
		t.Errorf("after a key press the open chat should be read: unread = %d", got)
	}
}

func TestFocusHintShown(t *testing.T) {
	h := newHarness(t, true)
	m := h.model()
	m2 := New(m.ctx, Deps{Client: m.deps.Client, Store: m.deps.Store, Log: m.deps.Log, Config: m.deps.Config, FocusHint: "tmux set -g focus-events on"}, m.sub)
	h.m = m2
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	if !strings.Contains(h.view(), "focus-events on") {
		t.Errorf("focus hint not shown:\n%s", h.view())
	}
}

func TestEditsDeletesReactionsRender(t *testing.T) {
	h := newHarness(t, true)
	h.client.Emit(wa.Message{ID: "a2", Chat: ada, Sender: ada, Timestamp: day.Add(time.Minute), Kind: wa.KindText, Text: "secret plans"})
	h.client.Emit(wa.MessageEdited{Chat: ada, ID: "a1", Kind: wa.KindText, Text: "hello from ada!", At: day.Add(2 * time.Minute)})
	h.client.Emit(wa.MessageDeleted{Chat: ada, ID: "a2", At: day.Add(3 * time.Minute)})
	h.client.Emit(wa.Reaction{Chat: ada, MessageID: "a1", Sender: ada, Emoji: "🎉", At: day.Add(4 * time.Minute)})
	h.client.Emit(wa.Reaction{Chat: ada, MessageID: "a1", Sender: "me@s.whatsapp.net", FromMe: true, Emoji: "🎉", At: day.Add(5 * time.Minute)})
	h.settle()

	v := ansi.Strip(h.view())
	for _, want := range []string{"hello from ada! (edited)", "This message was deleted", "🎉 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "secret plans") {
		t.Error("deleted text still shown")
	}
}

func TestReadModeMapping(t *testing.T) {
	for setting, want := range map[string]wa.ReadMode{
		config.MarkReadLocalOnly:   wa.ReadLocal,
		config.MarkReadReceiptsOff: wa.ReadDevices,
		config.MarkReadReceiptsOn:  wa.ReadEveryone,
		"":                         wa.ReadLocal,
		"something-new":            wa.ReadLocal,
	} {
		if got := readMode(setting); got != want {
			t.Errorf("readMode(%q) = %q, want %q", setting, got, want)
		}
	}
}

// seedAttachment stores an incoming document in ada's chat.
func (h *harness) seedAttachment(id, name string) {
	h.t.Helper()
	m := wa.Message{ID: id, Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: wa.KindDocument, Text: name, Media: watest.SizedDocumentMedia(name, 2_500_000)}
	if _, err := h.st.SaveMessages(h.t.Context(), []wa.Message{m}); err != nil {
		h.t.Fatalf("seed: %v", err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
}

func TestOpenAttachment(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	h.seedAttachment("doc1", "report.pdf")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "report.pdf · 2.4 MB") || strings.Contains(v, "✓") {
		t.Fatalf("attachment should show its size, not cached yet:\n%s", v)
	}

	h.key("o")
	if h.model().screen != screenPicker || !strings.Contains(h.view(), "report.pdf") {
		t.Fatalf("picker not shown:\n%s", h.view())
	}
	h.key("enter")
	h.settle()
	if len(h.opened) != 1 || filepath.Ext(h.opened[0]) != ".pdf" {
		t.Fatalf("opened = %v", h.opened)
	}
	if data, err := os.ReadFile(h.opened[0]); err != nil || string(data) != "%PDF-1.7" {
		t.Errorf("downloaded content = %q, %v", data, err)
	}
	h.key("esc")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "✓") {
		t.Errorf("cached attachment should show ✓:\n%s", v)
	}
	if len(h.model().downloading) != 0 {
		t.Error("download still marked in flight")
	}
}

func TestOpenAttachmentProblems(t *testing.T) {
	h := newHarness(t, true)
	h.seedAttachment("gone", "old.pdf")
	h.client.DownloadErr = wa.ErrMediaExpired
	h.key("o")
	h.key("enter")
	h.settle()
	if v := h.view(); !strings.Contains(v, "no longer on WhatsApp's servers") {
		t.Errorf("expired notice missing:\n%s", v)
	}

	h2 := newHarness(t, true)
	h2.client.DownloadData = []byte("#!/bin/sh")
	h2.openErr = media.ErrNotOpenable
	h2.seedAttachment("sh", "run.command")
	h2.key("o")
	h2.key("enter")
	h2.settle()
	if v := h2.view(); !strings.Contains(v, "Not opened (unrecognised file type)") {
		t.Errorf("unrecognised-type notice missing:\n%s", v)
	}

	h3 := newHarness(t, true)
	h3.key("o")
	if h3.model().screen != screenMain || !strings.Contains(h3.view(), "No attachments") {
		t.Errorf("empty picker should just say so:\n%s", h3.view())
	}
}

func TestAttachSendsAndCaches(t *testing.T) {
	h := newHarness(t, true)
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.key("a")
	if h.model().screen != screenAttach {
		t.Fatalf("attach screen not open:\n%s", h.view())
	}
	h.key(src)
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	h.key("for you")
	h.key("enter")
	h.settle()

	sent := h.client.SentFiles()
	if len(sent) != 1 || sent[0].Path != src || sent[0].Caption != "for you" || sent[0].Chat != ada {
		t.Fatalf("sent = %+v", sent)
	}
	if _, ok := h.cache.Lookup(ada, "FILE1"); !ok {
		t.Error("own attachment should be cached under its message ID")
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "notes.txt") {
		t.Errorf("sent file not shown:\n%s", v)
	}
}

func TestAttachRejectsMissingFile(t *testing.T) {
	h := newHarness(t, true)
	h.key("a")
	h.key("/nope/missing.jpg")
	h.key("enter")
	if h.model().screen != screenAttach || !strings.Contains(h.view(), "No such file") {
		t.Errorf("missing file should be reported in place:\n%s", h.view())
	}
	if len(h.client.SentFiles()) != 0 {
		t.Error("sent a missing file")
	}
}

func TestGroupInfoAndMentions(t *testing.T) {
	h := newHarness(t, true)
	h.client.Own = "15550000000@s.whatsapp.net"
	h.client.Emit(wa.Connected{})
	h.addContact(wa.Contact{JID: ada, Name: "Ada"})
	h.client.Emit(wa.Groups{List: []wa.Group{{
		JID: book, Name: "Book club", Topic: "chapter 3 this week",
		Participants: []wa.Participant{{JID: ada, Admin: true}, {JID: bob}, {JID: "15550000000@s.whatsapp.net"}},
	}}})
	h.client.Emit(wa.Message{ID: "g1", Chat: book, Sender: bob, IsGroup: true, Timestamp: day.Add(5 * time.Hour),
		Kind: wa.KindText, Text: "@15551234567 and @15550000000 are you in? @19999999999"})
	h.settle()
	h.selectChat(book)

	v := ansi.Strip(h.view())
	for _, want := range []string{"Book club", "3 members", "@Ada and @you are you in? @19999999999"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}

	h.key("i")
	h.settle()
	v = ansi.Strip(h.view())
	for _, want := range []string{"chapter 3 this week", "3 members", "Ada  +15551234567  admin", "+15559876543", "(You)"} {
		if !strings.Contains(v, want) {
			t.Errorf("info missing %q:\n%s", want, v)
		}
	}
	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should close info")
	}
}

func TestPersonInfo(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: ada, Name: "Ada", PushName: "ada.l"})
	h.client.Emit(wa.Contacts{})
	h.settle()
	h.selectChat(ada)
	h.key("i")
	h.settle()
	v := ansi.Strip(h.view())
	for _, want := range []string{"Number:  +15551234567", "Calls themselves: ada.l"} {
		if !strings.Contains(v, want) {
			t.Errorf("person info missing %q:\n%s", want, v)
		}
	}
}

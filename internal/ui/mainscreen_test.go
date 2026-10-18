package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

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

func TestAttachmentKeysLiveInTheMessageBox(t *testing.T) {
	keysOf := func(bs []key.Binding) string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Help().Key)
		}
		return " " + strings.Join(out, " ") + " "
	}
	h := newHarness(t, true)
	if got := keysOf(h.model().contextHelp()); strings.Contains(got, "ctrl+o") || strings.Contains(got, " o ") || !strings.Contains(got, " i ") {
		t.Errorf("chat list hints %q: info yes, attachments no", got)
	}
	h.key("o") // no longer a list shortcut
	if h.model().screen != screenMain {
		t.Error("o on the chat list should do nothing now")
	}

	h.key("enter") // into the message box
	if got := keysOf(h.model().contextHelp()); !strings.Contains(got, " ctrl+o ") || !strings.Contains(got, " ctrl+t ") {
		t.Errorf("message box hints %q should offer ctrl+o and ctrl+t", got)
	}
	h.key("oa") // plain letters are typed
	if v := h.model().conv.input.Value(); v != "oa" || h.model().screen != screenMain {
		t.Errorf("o and a should be typed: input %q, screen %v", v, h.model().screen)
	}
	h.key("ctrl+t")
	if h.model().screen != screenAttach {
		t.Errorf("ctrl+t should start sending a file (screen %v)", h.model().screen)
	}

	h = newHarness(t, true)
	h.addContact(wa.Contact{JID: carol, Name: "Carol"})
	h.key("/")
	h.key("car")
	h.key("enter") // a draft, in the message box
	if got := keysOf(h.model().contextHelp()); !strings.Contains(got, " ctrl+t ") || strings.Contains(got, " ctrl+o ") {
		t.Errorf("draft hints %q should offer ctrl+t only", got)
	}

	m := h.model()
	m.setDraft(nil)
	m.conv.chat = store.ChatSummary{} // nothing selected (no chats yet)
	if got := keysOf(m.contextHelp()); strings.Contains(got, "ctrl+") {
		t.Errorf("no chat: hints %q should offer neither", got)
	}
}

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/wa"
)

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

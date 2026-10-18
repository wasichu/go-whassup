package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/wa"
)

// newNotifyHarness has a terminal that takes OSC 777 notifications and
// isn't focused.
func newNotifyHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, true)
	m := h.model()
	m.deps.Notify = termcaps.Notify{Protocol: termcaps.NotifyOSC777}
	h.m = m
	h.send(tea.BlurMsg{})
	return h
}

func (h *harness) incoming(id, chat, text string, at time.Time) {
	h.t.Helper()
	h.client.Emit(wa.Message{ID: id, Chat: chat, Sender: chat, Timestamp: at, Kind: wa.KindText, Text: text})
	h.settle()
}

func (h *harness) notifications() []string {
	var out []string
	for _, r := range h.raw {
		if strings.Contains(r, "]777;") {
			out = append(out, r)
		}
	}
	return out
}

func TestNotifiesWhileUnfocused(t *testing.T) {
	h := newNotifyHarness(t)
	h.incoming("n1", bob, "are you there?", time.Now())
	if got := h.notifications(); len(got) != 1 || got[0] != "\x1b]777;notify;Bob;are you there?\x1b\\" {
		t.Fatalf("notifications = %q", got)
	}

	h.incoming("n2", bob, "hello??", time.Now()) // same chat within the cooldown
	if n := len(h.notifications()); n != 1 {
		t.Errorf("burst raised %d notifications, want 1", n)
	}
	h.incoming("old", ada, "from yesterday", time.Now().Add(-time.Hour)) // backlog after reconnect
	if n := len(h.notifications()); n != 1 {
		t.Errorf("backlog notified (%d)", n)
	}

	h.send(tea.FocusMsg{})
	h.incoming("n3", bob, "seen it?", time.Now()) // focused: the app shows it
	if n := len(h.notifications()); n != 1 {
		t.Errorf("notified while focused (%d)", n)
	}
	h.send(tea.BlurMsg{})
	h.incoming("n4", bob, "now?", time.Now()) // away again: cooldown was reset
	if n := len(h.notifications()); n != 2 {
		t.Errorf("after returning and leaving again: %d notifications, want 2", n)
	}
}

func TestNoNotificationForMutedChatsOrOwnMessages(t *testing.T) {
	h := newNotifyHarness(t)
	h.client.Emit(wa.ChatMuted{Chat: bob, Until: wa.MutedForever})
	h.settle()
	h.incoming("m1", bob, "muted", time.Now())
	h.client.Emit(wa.Message{ID: "me1", Chat: ada, Sender: ada, FromMe: true, Timestamp: time.Now(), Kind: wa.KindText, Text: "mine"})
	h.settle()
	if got := h.notifications(); len(got) != 0 {
		t.Errorf("notifications = %q", got)
	}
}

func TestNotificationPreviewAndDisable(t *testing.T) {
	for _, tt := range []struct {
		preview string
		enabled bool
		want    string
	}{
		{config.PreviewSender, true, "\x1b]777;notify;Bob;New message\x1b\\"},
		{config.PreviewNone, true, "\x1b]777;notify;go-whassup;New message\x1b\\"},
		{config.PreviewFull, false, ""},
	} {
		h := newNotifyHarness(t)
		m := h.model()
		m.deps.Config.Notifications = config.Notifications{Enabled: tt.enabled, Preview: tt.preview}
		h.m = m
		h.incoming("p1", bob, "secret plans", time.Now())
		got := strings.Join(h.notifications(), "")
		if got != tt.want {
			t.Errorf("%s enabled=%v: got %q, want %q", tt.preview, tt.enabled, got, tt.want)
		}
	}
}

func TestNotifySequenceIsSafe(t *testing.T) {
	// A sender can't end the sequence early, start another, or (OSC 777)
	// add fields through the title.
	evil := "a\x1b]0;pwned\x07b\x9c;c\nd"
	for _, n := range []termcaps.Notify{{Protocol: termcaps.NotifyOSC777}, {Protocol: termcaps.NotifyOSC99}, {Protocol: termcaps.NotifyOSC9}} {
		seq := notifySequence(n, evil, evil)
		if strings.Count(seq, "\x1b") != 2*strings.Count(seq, "\x1b\\") || strings.ContainsAny(seq, "\x07\n") || strings.Contains(seq, "\x9c") {
			t.Errorf("%v: unsafe sequence %q", n.Protocol, seq)
		}
	}
	if seq := notifySequence(termcaps.Notify{Protocol: termcaps.NotifyOSC777}, "x;y", "b;c"); seq != "\x1b]777;notify;x,y;b;c\x1b\\" {
		t.Errorf("777 = %q", seq)
	}
	if seq := notifySequence(termcaps.Notify{Protocol: termcaps.NotifyOSC9}, "Ada", "4;2"); seq != "\x1b]9;Ada: 4,2\x1b\\" {
		t.Errorf("9 = %q", seq)
	}
	if seq := notifySequence(termcaps.Notify{Protocol: termcaps.NotifyOSC9, Tmux: true}, "Ada", "hi"); !strings.HasPrefix(seq, "\x1bPtmux;\x1b\x1b]9;") {
		t.Errorf("tmux = %q", seq)
	}
	if got := cleanNotifyText(strings.Repeat("é", 300), 10); got != strings.Repeat("é", 9)+"…" {
		t.Errorf("truncate = %q", got)
	}
}

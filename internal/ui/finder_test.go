package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

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

// chatRows returns the chat list's rows (left pane, under its title).
func chatRows(view string) []string {
	var rows []string
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		left, _, pane := strings.Cut(line, "│")
		if i > 0 && pane && strings.TrimSpace(left) != "" {
			rows = append(rows, strings.TrimSpace(left))
		}
	}
	return rows
}

func TestDraftShowsInChatList(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: carol, Name: "Carol"})
	h.key("/")
	h.key("car")
	h.key("enter")
	h.key("esc") // out of the message box, onto the list
	rows := chatRows(h.view())
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "Carol") || !strings.HasSuffix(rows[0], "new") {
		t.Fatalf("draft should be the top row, marked new: %q", rows)
	}
	if m := h.model(); m.draft != carol || m.conv.chat.JID != carol || m.focus != focusList {
		t.Fatalf("draft=%q chat=%q focus=%v", m.draft, m.conv.chat.JID, m.focus)
	}

	h.key("up") // already the top row
	if m := h.model(); m.draft != carol {
		t.Errorf("up left the draft")
	}
	h.key("down") // to the first real chat; the unsent draft goes away
	h.settle()
	m := h.model()
	rows = chatRows(h.view())
	if m.draft != "" || m.conv.chat.JID != ada || len(rows) != 2 || !strings.HasPrefix(rows[0], "Ada") {
		t.Errorf("after down: draft=%q chat=%q rows=%q", m.draft, m.conv.chat.JID, rows)
	}
}

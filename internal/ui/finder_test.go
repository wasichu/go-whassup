package ui

import (
	"strings"
	"testing"

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

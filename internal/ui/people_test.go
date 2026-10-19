package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

const (
	stranger = "15550001111@s.whatsapp.net" // not in contacts; chose a name
	hidden   = "98765432109876@lid"         // privacy ID, no name
)

// groupWithStrangers is a group where a non-contact (with a chosen name)
// and someone known only by privacy ID have posted. It's open, focused.
func groupWithStrangers(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, true)
	if err := h.st.SaveGroups(t.Context(), []wa.Group{{JID: book, Name: "Book club"}}); err != nil {
		t.Fatal(err)
	}
	at := day.Add(time.Hour)
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "s1", Chat: book, Sender: stranger, PushName: "Bobby", IsGroup: true, Timestamp: at, Kind: wa.KindText, Text: "hi all"},
		{ID: "s2", Chat: book, Sender: hidden, IsGroup: true, Timestamp: at.Add(time.Minute), Kind: wa.KindText, Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(book)
	h.key("enter")
	return h
}

func TestStrangersNamedWithTildeOrUnknown(t *testing.T) {
	h := groupWithStrangers(t)
	v := ansi.Strip(h.view())
	for _, want := range []string{"~Bobby: hi all", "unknown member: hello"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "98765432109876") {
		t.Errorf("a privacy ID shouldn't be shown as if it were a number:\n%s", v)
	}
}

func TestSenderCard(t *testing.T) {
	h := groupWithStrangers(t)
	h.key("ctrl+r")
	h.key("up") // Bobby's message
	h.key("i")
	h.settle()
	v := ansi.Strip(h.view())
	for _, want := range []string{"Not in your contacts", "Calls themselves:    ~Bobby", "Number:              +15550001111", "c copy number"} {
		if !strings.Contains(v, want) {
			t.Errorf("card missing %q:\n%s", want, v)
		}
	}
	h.key("c")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Copied to the clipboard") {
		t.Errorf("c should copy the number:\n%s", v)
	}
	h.key("esc")
	if m := h.model(); m.screen != screenMain || !m.conv.picking {
		t.Errorf("esc should return to picking: screen %v picking %v", m.screen, m.conv.picking)
	}

	h.key("down") // the privacy-ID sender
	h.key("i")
	h.settle()
	v = ansi.Strip(h.view())
	if !strings.Contains(v, "hidden by WhatsApp") || strings.Contains(v, "copy number") {
		t.Errorf("a privacy ID has no number to show or copy:\n%s", v)
	}
}

func TestMemberLines(t *testing.T) {
	for _, tt := range []struct {
		mem  store.Member
		want string
		dim  bool // the name is a fallback, dimmed
	}{
		{store.Member{JID: ada, Name: "Ada"}, "  Ada  +15551234567", false},
		{store.Member{JID: stranger}, "  +15550001111", true},
		{store.Member{JID: hidden, Name: "~Zed"}, "  ~Zed", false}, // no number to show
		{store.Member{JID: hidden}, "  unknown member", true},
	} {
		got := memberLine(tt.mem, "")
		if ansi.Strip(got) != tt.want {
			t.Errorf("memberLine(%+v) = %q, want %q", tt.mem, ansi.Strip(got), tt.want)
		}
		if dimmed := strings.Contains(got, faintStyle.Render(strings.TrimSpace(tt.want))); dimmed != tt.dim {
			t.Errorf("memberLine(%+v) dimmed = %v, want %v", tt.mem, dimmed, tt.dim)
		}
	}
}

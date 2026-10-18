package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

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

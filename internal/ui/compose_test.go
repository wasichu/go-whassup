package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

// openAda opens Ada's chat and focuses the message box.
func openAda(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, true)
	h.selectChat(ada)
	h.key("enter")
	return h
}

func TestReplyToAMessage(t *testing.T) {
	h := openAda(t)
	h.key("ctrl+r")
	if c := h.model().conv; !c.picking || c.highlight != "a1" {
		t.Fatalf("ctrl+r should pick the newest message: picking=%v highlight=%q", c.picking, c.highlight)
	}
	h.key("r")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "↪ Replying to Ada: hello from ada") {
		t.Fatalf("reply banner missing:\n%s", v)
	}
	h.key("esc") // cancels the reply, stays in the box
	if m := h.model(); m.conv.replyTo != nil || m.focus != focusInput {
		t.Fatalf("esc should cancel the reply first: replyTo=%v focus=%v", m.conv.replyTo, m.focus)
	}

	h.key("ctrl+r")
	h.key("r")
	h.key("hi back")
	h.key("enter")
	h.settle()
	sent := h.client.SentMessages()
	if len(sent) != 1 || sent[0].Text != "hi back" || sent[0].ReplyTo == nil || *sent[0].ReplyTo != (wa.Reply{ID: "a1", Sender: ada}) {
		t.Fatalf("sent = %+v", sent)
	}
	v := ansi.Strip(h.view())
	if strings.Contains(v, "Replying to") || !strings.Contains(v, "↪ Ada: hello from ada") {
		t.Errorf("after sending: banner gone, the reply quoting Ada:\n%s", v)
	}
}

func TestPickingSkipsDeletedAndEscBacksOut(t *testing.T) {
	h := newHarness(t, true)
	at := day.Add(time.Hour)
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "a2", Chat: ada, Sender: ada, Timestamp: at, Kind: wa.KindText, Text: "gone soon"},
		{ID: "a3", Chat: ada, Sender: ada, Timestamp: at.Add(time.Minute), Kind: wa.KindText, Text: "latest"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.DeleteMessage(t.Context(), wa.MessageDeleted{Chat: ada, ID: "a2", At: at.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(ada)
	h.key("enter")
	h.key("ctrl+r")
	h.key("up") // past the deleted a2
	if hl := h.model().conv.highlight; hl != "a1" {
		t.Errorf("up should skip the deleted message to a1, got %q", hl)
	}
	h.key("esc")
	if c := h.model().conv; c.picking || c.highlight != "" {
		t.Errorf("esc should end picking and clear the highlight: %+v", c.compose)
	}
	h.key("re") // not picking: plain letters
	if v := h.model().conv.input.Value(); v != "re" {
		t.Errorf("r and e should type when not picking, input = %q", v)
	}
}

func TestReactWithQuickReactionAndRemove(t *testing.T) {
	h := openAda(t)
	h.key("ctrl+r")
	h.key("e")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "React:") || !strings.Contains(v, "👍") {
		t.Fatalf("reaction strip missing:\n%s", v)
	}
	h.key("2") // ❤️
	h.settle()
	if rs := h.client.SentReactions(); len(rs) != 1 || rs[0].Emoji != "❤️" || rs[0].MessageID != "a1" {
		t.Fatalf("reactions = %+v", rs)
	}
	h.waitFor("the reaction shown", func() bool { return strings.Contains(h.view(), "❤️") })

	// The strip starts on your current reaction: enter again removes it.
	h.key("ctrl+r")
	h.key("e")
	h.key("enter")
	h.settle()
	if rs := h.client.SentReactions(); len(rs) != 2 || rs[1].Emoji != "" {
		t.Errorf("choosing your own reaction again should remove it: %+v", rs)
	}
}

func TestReactWithAnyEmoji(t *testing.T) {
	h := openAda(t)
	h.key("ctrl+r")
	h.key("e")
	h.key("+")
	if h.model().screen != screenEmoji {
		t.Fatalf("+ should open emoji search:\n%s", ansi.Strip(h.view()))
	}
	h.key("laugh")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "😂") || !strings.Contains(v, "React to Ada") {
		t.Fatalf("search results missing:\n%s", v)
	}
	for i := 0; h.model().emojiPick.results[h.model().emojiPick.selected].Glyph != "😂"; i++ {
		if i == 3 {
			t.Fatal("😂 should be among the first results for laugh")
		}
		h.key("down")
	}
	h.key("enter")
	h.settle()
	if rs := h.client.SentReactions(); len(rs) != 1 || rs[0].Emoji != "😂" {
		t.Errorf("reactions = %+v", rs)
	}
	if h.model().screen != screenMain {
		t.Error("reacting should return to the chat")
	}
}

func TestNothingToPickOnADraft(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: carol, Name: "Carol"})
	h.key("/")
	h.key("car")
	h.key("enter") // a draft: no messages
	h.key("ctrl+r")
	if h.model().conv.picking {
		t.Error("a draft has nothing to pick")
	}
}

func TestQuotesNameMentions(t *testing.T) {
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: ada, Name: "Ada"})
	digits, _, _ := strings.Cut(ada, "@")
	at := day.Add(time.Hour)
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "m1", Chat: ada, Sender: ada, Timestamp: at, Kind: wa.KindText, Text: "ask @" + digits},
		{ID: "m2", Chat: ada, Sender: ada, Timestamp: at.Add(time.Minute), Kind: wa.KindText, Text: "ok",
			ReplyTo: &wa.Reply{ID: "m1", Sender: ada}},
	}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(ada)
	if v := ansi.Strip(h.view()); !strings.Contains(v, "↪ Ada: ask @Ada") {
		t.Fatalf("the quote should name the mention:\n%s", v)
	}
	h.key("enter")
	h.key("ctrl+r")
	h.key("up")
	h.key("r")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Replying to Ada: ask @Ada") {
		t.Errorf("the banner should name the mention:\n%s", v)
	}
}

func TestNarrowStatusDropsWholeHints(t *testing.T) {
	h := openAda(t)
	h.send(tea.WindowSizeMsg{Width: 70, Height: 20})
	lines := strings.Split(ansi.Strip(h.view()), "\n")
	status := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.HasSuffix(status, "…") || strings.Contains(status, "reply/r…") {
		t.Errorf("hints should end with a whole one, then …: %q", status)
	}
}

func TestPlusOpensEmojiSearch(t *testing.T) {
	// The kitty keyboard protocol reports "+" as shift+= with "+" shifted.
	kittyPlus := tea.KeyPressMsg{Code: '=', ShiftedCode: '+', Mod: tea.ModShift}
	for name, keys := range map[string][]string{"from the strip": {"ctrl+r", "e"}, "while picking": {"ctrl+r"}} {
		t.Run(name, func(t *testing.T) {
			h := openAda(t)
			for _, k := range keys {
				h.key(k)
			}
			h.send(kittyPlus)
			if h.model().screen != screenEmoji || h.model().emojiPick.target.ID != "a1" {
				t.Errorf("+ should open emoji search for a1:\n%s", ansi.Strip(h.view()))
			}
		})
	}
}

func TestCopySelectedMessageText(t *testing.T) {
	h := openAda(t)
	h.key("ctrl+r")
	m, cmd := h.model().pickKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard("hello from ada")()) {
		t.Fatalf("c should copy the message text; got %v", cmd)
	}
	if m.notice != copiedNotice {
		t.Errorf("notice = %q", m.notice)
	}
	if m, _ = m.pickKey(tea.KeyPressMsg{Code: tea.KeyUp}); m.notice != "" {
		t.Errorf("the next key should clear the notice, got %q", m.notice)
	}

	photo := wa.Message{ID: "p1", Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: wa.KindImage}
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{photo}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.key("esc")
	h.key("ctrl+r") // the uncaptioned photo, now newest
	if m, cmd = h.model().pickKey(tea.KeyPressMsg{Code: 'c', Text: "c"}); cmd != nil || m.notice != noTextNotice {
		t.Errorf("a photo without a caption has nothing to copy: cmd %v, notice %q", cmd, m.notice)
	}
}

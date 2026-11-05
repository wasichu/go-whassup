package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

func mentionHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, true)
	h.addContact(wa.Contact{JID: ada, Name: "Ada Lovelace"})
	h.addContact(wa.Contact{JID: bob, Name: "Bob"})
	h.client.Emit(wa.Groups{List: []wa.Group{{JID: book, Name: "Book club", Participants: []wa.Participant{{JID: ada}, {JID: bob}}}}})
	h.client.Emit(wa.Message{ID: "group-note", Chat: book, Sender: bob, IsGroup: true, Timestamp: day, Kind: wa.KindText, Text: "hello"})
	h.settle()
	h.selectChat(book)
	h.key("enter")
	return h
}

func TestMentionCompletionSendsMetadataAndPreservesDraft(t *testing.T) {
	h := mentionHarness(t)
	h.key("Hi @ad")
	if !strings.Contains(h.view(), "Ada Lovelace") || len(h.model().conv.completion.matches) != 1 {
		t.Fatalf("missing suggestion: %s", h.view())
	}
	h.key("enter")
	if got := h.model().conv.input.Value(); got != "Hi @Ada Lovelace " || len(h.client.SentMessages()) != 0 {
		t.Fatalf("accept must insert without sending: %q", got)
	}
	h.key("ctrl+p")
	h.key("esc")
	h.key("f1")
	h.key("esc")
	h.key("hello")
	h.key("enter")
	h.settle()
	sent := h.client.SentMessages()
	calls := h.client.MentionCalls()
	if len(sent) != 1 || sent[0].Text != "Hi @15551234567 hello" || !slices.Equal(calls[0], []string{ada}) {
		t.Fatalf("sent=%+v mentions=%v", sent, calls)
	}
}

func TestMentionReplyAndEdit(t *testing.T) {
	h := mentionHarness(t)
	h.key("ctrl+r")
	h.key("r")
	h.key("@bob")
	h.key("tab")
	h.key("yes")
	h.key("enter")
	h.settle()
	sent := h.client.SentMessages()
	if len(sent) != 1 || sent[0].ReplyTo == nil || !slices.Equal(h.client.MentionCalls()[0], []string{bob}) {
		t.Fatalf("reply missing metadata: %+v %v", sent, h.client.MentionCalls())
	}
	// Preserve an unsent named mention while opening and cancelling an edit.
	h.key("@ada")
	h.key("tab")
	draft := h.model().conv.input.Value()
	h.client.Emit(wa.Message{ID: "own-edit", Chat: book, FromMe: true, IsGroup: true, Timestamp: time.Now(), Kind: wa.KindText, Text: "old"})
	h.settle()
	h.key("ctrl+r")
	h.key("e")
	h.key("ctrl+a")
	h.key("ctrl+k") // clear using textinput's ordinary bindings
	h.key("@bob")
	h.key("tab")
	h.key("updated")
	h.key("enter")
	h.settle()
	calls := h.client.MentionCalls()
	if len(calls) != 2 || !slices.Equal(calls[1], []string{bob}) {
		t.Fatalf("edit missing metadata: %v", calls)
	}
	if h.model().conv.input.Value() != draft {
		t.Fatalf("edit lost stashed draft: %q", h.model().conv.input.Value())
	}
	h.key("enter")
	h.settle()
	calls = h.client.MentionCalls()
	if !slices.Equal(calls[len(calls)-1], []string{ada}) {
		t.Fatalf("edit lost stashed mention: %v", calls)
	}
}

func TestMentionDismissalAndPlainText(t *testing.T) {
	h := mentionHarness(t)
	h.key("@ad")
	h.key("esc")
	if h.model().focus != focusInput || len(h.model().conv.completion.matches) != 0 {
		t.Fatal("escape should close suggestions first")
	}
	h.key("enter")
	h.settle()
	if got := h.client.SentMessages()[0].Text; got != "@ad" || len(h.client.MentionCalls()[0]) != 0 {
		t.Fatalf("dismissed name became mention: %q", got)
	}
	h.key("mail@example.com")
	if len(h.model().conv.completion.matches) != 0 {
		t.Fatal("email triggered mention completion")
	}
	direct := openAda(t)
	direct.key("@ad")
	if len(direct.model().conv.completion.matches) != 0 {
		t.Fatal("direct chat showed group members")
	}
}

func TestMentionSpansUnicodeEditsAndMembership(t *testing.T) {
	c := newConversation()
	c.chat = store.ChatSummary{JID: book, IsGroup: true}
	c.completion.members = []store.Member{{JID: ada, Name: "Áda Lovelace"}, {JID: "123456789012345@lid", Name: "Mystery"}}
	c.input.SetValue("🙂 @Ád tail")
	c.input.SetCursor(5)
	c.refreshCompletion()
	if len(c.completion.matches) != 1 {
		t.Fatalf("unicode match missing: %+v", c.completion)
	}
	c.completeKey(tea.KeyPressMsg{Code: tea.KeyTab})
	text, ids := c.encodeMentions()
	if text != "🙂 @15551234567 tail" || !slices.Equal(ids, []string{ada}) {
		t.Fatalf("unicode replacement: %q %v", text, ids)
	}
	before := c.input.Value()
	c.input.SetValue("prefix " + before)
	c.trackMentions(before)
	_, ids = c.encodeMentions()
	if !slices.Equal(ids, []string{ada}) {
		t.Fatal("insertion before span lost mention")
	}
	before = c.input.Value()
	c.input.SetValue(strings.Replace(before, "Áda", "Different", 1))
	c.trackMentions(before)
	_, ids = c.encodeMentions()
	if len(ids) != 0 {
		t.Fatal("editing a mention label kept a hidden recipient")
	}
	c.input.SetValue("@Myst")
	c.input.CursorEnd()
	c.refreshCompletion()
	c.completeKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	text, ids = c.encodeMentions()
	if text != "@123456789012345 " || !slices.Equal(ids, []string{"123456789012345@lid"}) {
		t.Fatalf("LID mention: %q %v", text, ids)
	}
	c.completion.members = nil
	text, ids = c.encodeMentions()
	if text != "@Mystery " || len(ids) != 0 {
		t.Fatal("departed member retained hidden metadata")
	}
}

func TestMentionIdenticalNamesKeepCorrectRecipientAfterDeletion(t *testing.T) {
	c := newConversation()
	c.chat = store.ChatSummary{JID: book, IsGroup: true}
	c.completion.members = []store.Member{{JID: ada, Name: "Alex"}, {JID: bob, Name: "Alex"}}
	c.input.Focus()
	c.input.SetValue("@Alex @Alex ")
	c.draftMentions = []draftMention{{0, 5, ada}, {6, 11, bob}}
	c.input.SetCursor(6)
	c.updateInput(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	text, ids := c.encodeMentions()
	if strings.TrimSpace(text) != "@15559876543" || !slices.Equal(ids, []string{bob}) {
		t.Fatalf("deleting the first Alex changed the recipient: %q %v", text, ids)
	}
}

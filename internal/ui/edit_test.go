package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

// mineWithAda opens Ada's chat with two of your messages after hers: one
// sent three days ago (too old to change) and one a minute ago, the newest.
func mineWithAda(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, true)
	now := time.Now()
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "old", Chat: ada, Sender: "me@s.whatsapp.net", FromMe: true, Timestamp: now.Add(-72 * time.Hour), Kind: wa.KindText, Text: "from before"},
		{ID: "new", Chat: ada, Sender: "me@s.whatsapp.net", FromMe: true, Timestamp: now.Add(-time.Minute), Kind: wa.KindText, Text: "see you at 5"},
	}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(ada)
	h.key("enter")
	return h
}

func TestDeleteForEveryone(t *testing.T) {
	h := mineWithAda(t)
	h.key("ctrl+r") // the newest: yours, a minute old
	h.key("d")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Delete this message for everyone?") {
		t.Fatalf("d should ask first:\n%s", v)
	}
	h.key("n")
	if c := h.model().conv; c.confirmingDelete || !c.picking || len(h.client.SentDeletes()) != 0 {
		t.Fatalf("n should keep the message and go back to picking: %+v", c.compose)
	}
	h.key("d")
	h.key("y")
	h.settle()
	if ds := h.client.SentDeletes(); len(ds) != 1 || ds[0].ID != "new" || ds[0].Chat != ada {
		t.Fatalf("deletes sent = %+v", ds)
	}
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "This message was deleted") || strings.Contains(v, "see you at 5") {
		t.Errorf("the message should show as deleted:\n%s", v)
	}
	if h.model().conv.picking {
		t.Error("deleting should end picking")
	}
}

func TestDeleteRefusesOthersAndOldMessages(t *testing.T) {
	h := mineWithAda(t)
	h.key("ctrl+r")
	h.key("up") // yours, three days old
	h.key("d")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "within 2 days") || strings.Contains(v, "for everyone?") {
		t.Errorf("an old message should say why it can't be deleted:\n%s", v)
	}
	h.key("up") // Ada's
	h.key("d")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "only delete your own messages") {
		t.Errorf("someone else's message should say why it can't be deleted:\n%s", v)
	}
	if ds := h.client.SentDeletes(); len(ds) != 0 {
		t.Errorf("nothing should be deleted: %+v", ds)
	}
}

package headless

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

func TestDBCommands(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "messages.db"), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	var out bytes.Buffer
	c := newConsole("", &out)
	if err := c.Chats(t.Context(), st); err != nil {
		t.Fatalf("Chats on empty store: %v", err)
	}
	if !strings.Contains(out.String(), "No chats stored yet") {
		t.Errorf("empty output = %q", out.String())
	}

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := st.SaveMessages(t.Context(), []wa.Message{
		{ID: "1", Chat: "1@s.whatsapp.net", Sender: "1@s.whatsapp.net", PushName: "Ada", Timestamp: at, Kind: wa.KindText, Text: "hi"},
		{ID: "2", Chat: "1@s.whatsapp.net", Sender: "me", FromMe: true, Timestamp: at.Add(time.Minute), Kind: wa.KindImage, Text: "cat"},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
	if err := st.SaveChats(t.Context(), []wa.Chat{{JID: "1@s.whatsapp.net", Name: "Ada"}}); err != nil {
		t.Fatalf("SaveChats: %v", err)
	}

	out.Reset()
	if err := c.Chats(t.Context(), st); err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if !strings.Contains(out.String(), "Ada") || !strings.Contains(out.String(), "1@s.whatsapp.net") {
		t.Errorf("chats output:\n%s", out.String())
	}

	out.Reset()
	if err := c.Messages(t.Context(), st, "ada", 10); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	for _, want := range []string{"Ada (1@s.whatsapp.net), 2 of 2 messages", "Ada: hi", "me: [image] cat"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("messages output missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := c.Search(t.Context(), st, "hi", 0); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out.String(), "Ada: [hi]") {
		t.Errorf("search output:\n%s", out.String())
	}

	if err := c.Messages(t.Context(), st, "nobody", 10); err == nil {
		t.Error("expected error for unknown chat")
	}
}

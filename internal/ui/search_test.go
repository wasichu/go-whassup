package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

func TestSearchScreen(t *testing.T) {
	h := newHarness(t, true)
	h.key("s")
	if h.model().screen != screenSearch {
		t.Fatalf("search not open:\n%s", h.view())
	}
	h.key("bob")
	h.settle()
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "Bob") || !strings.Contains(v, "hi from bob") {
		t.Fatalf("hit missing:\n%s", v)
	}
	h.key("enter")
	h.settle()
	m := h.model()
	if m.screen != screenMain || m.conv.chat.JID != bob || m.conv.highlight != "b1" {
		t.Errorf("screen=%v chat=%q highlight=%q", m.screen, m.conv.chat.JID, m.conv.highlight)
	}

	h.key("s")
	h.key("zzzz")
	h.settle()
	if !strings.Contains(h.view(), "No messages match") {
		t.Errorf("no-match message missing:\n%s", h.view())
	}
}

func TestSearchJumpsToOlderMessage(t *testing.T) {
	h := newHarness(t, true)
	h.send(tea.WindowSizeMsg{Width: 90, Height: 16})
	var msgs []wa.Message
	for i := range 40 {
		text := fmt.Sprintf("filler %d", i)
		if i == 3 {
			text = "the needle"
		}
		msgs = append(msgs, wa.Message{ID: fmt.Sprintf("n%d", i), Chat: ada, Sender: ada, Timestamp: day.Add(time.Duration(i) * time.Minute), Kind: wa.KindText, Text: text})
	}
	if _, err := h.st.SaveMessages(t.Context(), msgs); err != nil {
		t.Fatal(err)
	}
	h.key("s")
	h.key("needle")
	h.settle()
	h.key("enter")
	h.settle()
	if v := ansi.Strip(h.view()); !strings.Contains(v, "the needle") {
		t.Errorf("should scroll to the hit:\n%s", v)
	}
}

// encryptedStore is a store whose search is off, as while encrypted.
type encryptedStore struct{ Store }

func (encryptedStore) Search(context.Context, string, int) ([]store.SearchHit, error) {
	return nil, store.ErrSearchUnavailable
}

func TestSearchUnavailableShownOnOpen(t *testing.T) {
	h := newHarness(t, true)
	m := h.model()
	m.deps.Store = encryptedStore{m.deps.Store}
	h.m = m
	h.key("s")
	h.settle()
	if v := ansi.Strip(h.view()); !strings.Contains(v, "unavailable while encryption is on") {
		t.Fatalf("unavailable not shown before typing:\n%s", v)
	}
	h.key("ab") // typing, then erasing it all, must not hide it
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.settle()
	if v := ansi.Strip(h.view()); !strings.Contains(v, "unavailable while encryption is on") {
		t.Fatalf("unavailable hidden after clearing the query:\n%s", v)
	}
}

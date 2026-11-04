package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestActionsPreserveDraftAndRunVoice(t *testing.T) {
	h, audio := newVoiceHarness(t)
	h.key("unsent draft?")
	h.key("ctrl+p")
	h.key("keyboard") // j/k and spaces must remain searchable text.
	if h.model().actions.input.Value() != "keyboard" || len(h.model().actions.matches) != 1 {
		t.Fatalf("action search failed: %s", h.view())
	}
	h.key("esc")
	if h.model().conv.input.Value() != "unsent draft?" {
		t.Fatal("menu lost draft")
	}
	h.key("ctrl+p")
	h.key("record voice")
	h.key("enter")
	h.waitFor("recording started", func() bool { n, _ := audio.counts(); return n == 1 })
	if h.model().screen != screenVoice {
		t.Fatal("action did not open recorder")
	}
	h.key("esc")
	if h.model().conv.input.Value() != "unsent draft?" || len(h.client.SentVoices()) != 0 {
		t.Fatal("action discarded or sent the text draft")
	}
}

func TestSelectedMessageActionsAndCompactHints(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter")
	h.key("draft")
	h.key("ctrl+r")
	picked := h.model().conv.picked
	for _, b := range h.model().contextHelp() {
		if b.Help().Key == "enter" && b.Help().Desc != "open/play" {
			t.Fatal("long enter hint")
		}
		if b.Help().Key == "i" && b.Help().Desc != "info" {
			t.Fatal("long info hint")
		}
	}
	h.key("ctrl+p")
	h.key("esc")
	if !h.model().conv.picking || h.model().conv.picked != picked {
		t.Fatal("menu lost selection")
	}
	h.key("ctrl+p")
	h.key("sender")
	h.key("enter")
	if h.model().screen != screenSender {
		t.Fatalf("message action did not open info: %s", h.view())
	}
	h.key("esc")
	if h.model().conv.picked != picked || h.model().conv.input.Value() != "draft" {
		t.Fatal("message action lost selection or draft")
	}
}

func TestGlobalHelpPreservesScreenAndScrolls(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter")
	h.key("draft?")
	h.send(tea.WindowSizeMsg{Width: 60, Height: 16})
	h.key("f1")
	if !h.model().showHelp {
		t.Fatal("F1 did not open help while composing")
	}
	before := h.view()
	for range 20 {
		h.key("pgdown")
	}
	if h.view() == before || !strings.Contains(h.view(), "ctrl+c") {
		t.Fatalf("help did not scroll to last keys: %s", h.view())
	}
	for _, line := range strings.Split(h.view(), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("help exceeds terminal width: %q", line)
		}
	}
	h.key("f1")
	if h.model().showHelp || h.model().conv.input.Value() != "draft?" {
		t.Fatal("help changed draft")
	}
	h.key("ctrl+p")
	h.key("link")
	h.key("f1")
	if !h.model().showHelp {
		t.Fatal("F1 did not work in actions menu")
	}
	h.key("esc")
	if h.model().screen != screenActions || h.model().actions.input.Value() != "link" {
		t.Fatal("help changed actions query")
	}
}

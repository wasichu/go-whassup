package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		_, cmd := New().Update(key)
		if cmd == nil {
			t.Fatalf("%q: expected quit command, got nil", key.String())
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q: expected tea.QuitMsg", key.String())
		}
	}
}

func TestWindowSizeIsRecorded(t *testing.T) {
	updated, _ := New().Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m := updated.(Model)
	if m.width != 80 || m.height != 24 {
		t.Errorf("size = %dx%d, want 80x24", m.width, m.height)
	}
}

func TestViewShowsStatus(t *testing.T) {
	v := New().View()
	if !strings.Contains(v.Content, "Not connected") {
		t.Errorf("view missing connection status:\n%s", v.Content)
	}
	if !v.AltScreen {
		t.Error("expected alt screen")
	}
}

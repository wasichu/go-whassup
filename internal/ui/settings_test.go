package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/config"
)

func TestSettingsToggleEditValidateSave(t *testing.T) {
	h := newHarness(t, true)
	h.key(",")
	v := h.view()
	for _, want := range []string{"Settings", "[history]", "request_full_sync", "keep_days", "Applies the next time you link"} {
		if !strings.Contains(v, want) {
			t.Errorf("settings view missing %q:\n%s", want, v)
		}
	}

	// full_sync_days without request_full_sync is invalid: kept, error shown.
	h.key("down")
	h.key("enter")
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("30")
	h.key("enter")
	if !strings.Contains(h.view(), "only used with") || len(h.applied) != 0 {
		t.Fatalf("cross-field validation not shown or config applied:\n%s", h.view())
	}
	h.key("esc") // cancel the edit

	// Toggle request_full_sync with space: saved and applied.
	h.key("up")
	h.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if len(h.applied) != 1 || !h.applied[0].History.RequestFullSync {
		t.Fatalf("toggle not applied: %+v\n%s", h.applied, h.view())
	}
	saved, err := config.Load(h.cfgPath)
	if err != nil || !saved.History.RequestFullSync {
		t.Fatalf("toggle not saved: %+v, %v", saved, err)
	}
	if !strings.Contains(h.view(), "Saved to") {
		t.Errorf("save confirmation missing:\n%s", h.view())
	}

	// Edit keep_days: junk rejected, then a valid value saved.
	h.key("down")
	h.key("down")
	h.key("enter")
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("x")
	h.key("enter")
	if !strings.Contains(h.view(), "whole number") {
		t.Errorf("parse error missing:\n%s", h.view())
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h.key("90")
	h.key("enter")
	if saved, err := config.Load(h.cfgPath); err != nil || saved.History.KeepDays != 90 {
		t.Errorf("keep_days not saved: %+v, %v", saved, err)
	}
	if got := h.model().deps.Config.History.KeepDays; got != 90 {
		t.Errorf("model config not updated: %d", got)
	}

	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should return to main")
	}
	h.key(",")
	if h.model().settings.cfg.History.KeepDays != 90 {
		t.Error("reopened settings lost the saved value")
	}
}

func TestSettingsConfirmsEncryptionToggle(t *testing.T) {
	h := newHarness(t, true)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24}) // the question must fit a small terminal
	h.key(",")
	for range 6 { // down to encryption.enabled
		h.key("down")
	}
	st := h.model().settings
	if f := st.cfg.Fields()[st.selected]; f.Name() != "encryption.enabled" {
		t.Fatalf("selected %s", f.Name())
	}
	h.key("enter")
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "who you talk to") || !strings.Contains(v, "y = yes, n = no") {
		t.Fatalf("confirmation with metadata warning missing:\n%s", v)
	}
	if len(h.applied) != 0 {
		t.Fatal("applied before confirming")
	}
	h.key("n")
	if len(h.applied) != 0 || h.model().settings.cfg.Encryption.Enabled {
		t.Fatal("n should cancel")
	}
	h.key("enter")
	h.key("y")
	if len(h.applied) != 1 || !h.applied[0].Encryption.Enabled {
		t.Errorf("y should apply: %+v", h.applied)
	}
}

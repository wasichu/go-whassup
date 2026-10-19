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
	h.waitSaves(1)
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
	if !strings.Contains(v, "Turn on encryption?") || !strings.Contains(v, "who you talk to") ||
		!strings.Contains(v, "y  Encrypt now") || !strings.Contains(v, "n  Cancel") {
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
	h.waitSaves(1)
	if len(h.applied) != 1 || !h.applied[0].Encryption.Enabled {
		t.Fatalf("y should apply: %+v", h.applied)
	}

	// Turning it off asks its own question, with choices that say what happens.
	h.key("enter")
	v = ansi.Strip(h.view())
	if !strings.Contains(v, "Turn off encryption?") || !strings.Contains(v, "decrypted back to plain text") ||
		!strings.Contains(v, "y  Decrypt now") || !strings.Contains(v, "n  Keep them encrypted") ||
		strings.Contains(v, "Turn on encryption?") {
		t.Fatalf("turning off should ask about decrypting:\n%s", v)
	}
	h.key("n")
	if len(h.applied) != 1 || !h.model().settings.cfg.Encryption.Enabled {
		t.Fatal("n should keep encryption on")
	}
	h.key("enter")
	h.key("y")
	h.waitSaves(2)
	if len(h.applied) != 2 || h.applied[1].Encryption.Enabled || h.model().settings.cfg.Encryption.Enabled {
		t.Errorf("y should turn it off: %+v", h.applied)
	}
}

func TestSettingsKeySourceAsksForPasswordCommand(t *testing.T) {
	h := newHarness(t, true)
	h.key(",")
	for range 7 { // down to encryption.key_source
		h.key("down")
	}
	if v := ansi.Strip(h.view()); hasSettingRow(v, "password_command") {
		t.Fatalf("password_command shown with key_source = keyring:\n%s", v)
	}

	h.key("enter") // keyring -> password-command reveals the empty command
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "‹ password-command ›") || !hasSettingRow(v, "password_command") || !h.model().settings.editing {
		t.Fatalf("command not asked for:\n%s", v)
	}
	h.key("enter") // nothing typed
	if v := ansi.Strip(h.view()); !strings.Contains(v, "password_command is required") || len(h.applied) != 0 {
		t.Fatalf("empty command accepted (applied %d):\n%s", len(h.applied), v)
	}
	h.key("esc") // drops the key_source change too
	v = ansi.Strip(h.view())
	if hasSettingRow(v, "password_command") || !strings.Contains(v, "‹ keyring ›") || len(h.applied) != 0 {
		t.Fatalf("esc should leave keyring unchanged:\n%s", v)
	}

	h.key("enter")
	h.key("pass show wa")
	h.key("enter")
	h.waitSaves(1)
	if n := len(h.applied); n != 1 || h.applied[0].Encryption.KeySource != "password-command" || h.applied[0].Encryption.PasswordCommand != "pass show wa" {
		t.Fatalf("applied = %+v", h.applied)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "pass show wa") {
		t.Errorf("saved command not listed:\n%s", v)
	}

	h.key("up") // password_command -> key_source
	h.key("right")
	h.waitSaves(2)
	if got := h.applied[len(h.applied)-1].Encryption.KeySource; got != "prompt" || h.model().settings.editing {
		t.Errorf("right: key_source = %q, editing = %v; want prompt with no question", got, h.model().settings.editing)
	}
	h.key("left") // back to password-command: its command is already set
	h.key("left") // and on to keyring
	h.waitSaves(4)
	if got := h.applied[len(h.applied)-1].Encryption.KeySource; got != "keyring" {
		t.Errorf("left twice: key_source = %q, want keyring", got)
	}
}

// hasSettingRow reports whether the Settings view lists key as a row (its
// name also appears in other settings' help text).
func hasSettingRow(view, key string) bool {
	for _, line := range strings.Split(view, "\n") {
		if f := strings.Fields(strings.TrimLeft(line, " ›")); len(f) > 0 && f[0] == key {
			return true
		}
	}
	return false
}

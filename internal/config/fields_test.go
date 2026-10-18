package config

import (
	"strings"
	"testing"
)

func TestFieldsDiscoveryAndEditing(t *testing.T) {
	cfg := Default()
	fields := cfg.Fields()
	var names []string
	for _, f := range fields {
		names = append(names, f.Name())
	}
	want := "history.request_full_sync history.full_sync_days history.keep_days history.max_messages_per_chat ui.inline_images privacy.mark_read encryption.enabled encryption.key_source notifications.enabled notifications.preview" // password_command only with key_source = password-command
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("fields = %s, want %s", got, want)
	}
	if !fields[0].PairingOnly || fields[2].PairingOnly {
		t.Error("PairingOnly tags not read")
	}
	if fields[2].Comment == "" || fields[0].Kind != KindBool || fields[2].Kind != KindInt {
		t.Errorf("field metadata = %+v", fields[2])
	}

	fields[0].Cycle()
	if err := fields[2].Set(" 30 "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !cfg.History.RequestFullSync || cfg.History.KeepDays != 30 {
		t.Errorf("edits didn't reach the config: %+v", cfg.History)
	}
	if fields[2].Value() != "30" || fields[0].Value() != "true" {
		t.Errorf("Value() = %q, %q", fields[2].Value(), fields[0].Value())
	}
	if err := fields[3].Set("lots"); err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Errorf("bad int accepted: %v", err)
	}
	if err := fields[0].Set("maybe"); err == nil {
		t.Error("bad bool accepted")
	}
}

func TestChoiceField(t *testing.T) {
	cfg := Default()
	var f Field
	for _, x := range cfg.Fields() {
		if x.Name() == "privacy.mark_read" {
			f = x
		}
	}
	if f.Kind != KindChoice || strings.Join(f.Choices, ",") != "local-only,read-receipts-off,read-receipts-on" || f.Value() != MarkReadLocalOnly {
		t.Fatalf("mark_read field = %+v (value %q)", f, f.Value())
	}
	f.Cycle()
	f.Cycle()
	if cfg.Privacy.MarkRead != MarkReadReceiptsOn {
		t.Errorf("after two cycles = %q", cfg.Privacy.MarkRead)
	}
	f.Cycle()
	if cfg.Privacy.MarkRead != MarkReadLocalOnly {
		t.Errorf("cycle should wrap, got %q", cfg.Privacy.MarkRead)
	}
	if err := f.Set("sometimes"); err == nil || !strings.Contains(err.Error(), "local-only, read-receipts-off, read-receipts-on") {
		t.Errorf("bad choice accepted: %v", err)
	}
	if err := f.Set(MarkReadReceiptsOff); err != nil || cfg.Privacy.MarkRead != MarkReadReceiptsOff {
		t.Errorf("Set(read-receipts-off) = %v, %q", err, cfg.Privacy.MarkRead)
	}
}

func TestEncryptionFields(t *testing.T) {
	cfg := Default()
	byName := map[string]Field{}
	for _, f := range cfg.Fields() {
		byName[f.Name()] = f
	}
	if f := byName["encryption.enabled"]; f.Kind != KindBool || !strings.Contains(f.Confirm, "who you talk to") {
		t.Errorf("enabled field = %+v; want a bool with the metadata warning as confirmation", f)
	}
	on, ok1 := byName["encryption.enabled"].ConfirmationFor("true")
	off, ok2 := byName["encryption.enabled"].ConfirmationFor("false")
	if !ok1 || !ok2 || on.Question != "Turn on encryption?" || on.Yes != "Encrypt now" ||
		off.Question != "Turn off encryption?" || off.Yes != "Decrypt now" || off.No != "Keep them encrypted" {
		t.Errorf("confirmations: on %+v, off %+v", on, off)
	}
	if _, ask := byName["ui.inline_images"].ConfirmationFor("false"); ask {
		t.Error("a setting without a confirm tag asked")
	}
	if f := byName["encryption.key_source"]; f.Kind != KindChoice || f.Value() != KeySourceKeyring {
		t.Errorf("key_source = %+v (%q)", f, f.Value())
	}
	if _, ok := byName["encryption.password_command"]; ok {
		t.Error("password_command listed while key_source is keyring")
	}
	cfg.Encryption.KeySource = KeySourcePasswordCommand
	for _, f := range cfg.Fields() {
		byName[f.Name()] = f
	}
	cmd, ok := byName["encryption.password_command"]
	if !ok || cmd.Kind != KindText {
		t.Fatalf("password_command = %+v, %v; want a text field with key_source = password-command", cmd, ok)
	}
	if err := cmd.Set("pass show whatsapp"); err != nil || cfg.Encryption.PasswordCommand != "pass show whatsapp" {
		t.Errorf("Set = %v, %q", err, cfg.Encryption.PasswordCommand)
	}

	// Required with that source even while encryption is off, so the file
	// never holds a source that can't work.
	cfg.Encryption.Enabled, cfg.Encryption.PasswordCommand = false, " "
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "password_command is required") {
		t.Errorf("Validate = %v", err)
	}

	src := byName["encryption.key_source"]
	src.Set(KeySourceKeyring) //nolint:errcheck // a listed choice
	src.CycleBack()
	if cfg.Encryption.KeySource != KeySourcePrompt {
		t.Errorf("CycleBack from keyring = %q, want prompt (wrapping)", cfg.Encryption.KeySource)
	}
}

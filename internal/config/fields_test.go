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
	want := "history.request_full_sync history.full_sync_days history.keep_days history.max_messages_per_chat"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("fields = %s, want %s", got, want)
	}
	if !fields[0].PairingOnly || fields[2].PairingOnly {
		t.Error("PairingOnly tags not read")
	}
	if fields[2].Comment == "" || fields[0].Kind != KindBool || fields[2].Kind != KindInt {
		t.Errorf("field metadata = %+v", fields[2])
	}

	fields[0].Toggle()
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

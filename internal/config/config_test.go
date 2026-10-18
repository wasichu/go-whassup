package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != Default() {
		t.Errorf("cfg = %+v, want defaults", cfg)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	want := Config{History: History{RequestFullSync: true, FullSyncDays: 365, KeepDays: 90, MaxMessagesPerChat: 5000}, UI: UI{InlineImages: false}, Privacy: Privacy{MarkRead: MarkReadReceiptsOn},
		Encryption: Encryption{KeySource: KeySourcePrompt}}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, s := range []string{"# go-whassup configuration", "[history]", "# Delete stored messages older than", "keep_days = 90"} {
		if !strings.Contains(string(data), s) {
			t.Errorf("saved file missing %q:\n%s", s, data)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %o, want 600", perm)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	path := writeConfig(t, "[history]\nkeep_days = 30\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.History.KeepDays != 30 || cfg.History.MaxMessagesPerChat != 0 || cfg.History.RequestFullSync || !cfg.UI.InlineImages {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, body, wantErr string
	}{
		{"unknown key", "[history]\nkeep_dayz = 3\n", "keep_dayz"},
		{"unknown section", "[histroy]\nkeep_days = 3\n", "histroy"},
		{"syntax", "[history\n", "config"},
		{"wrong type", "[history]\nkeep_days = \"lots\"\n", "config"},
		{"negative", "[history]\nkeep_days = -1\n", "keep_days must be 0 or more"},
		{"days without full sync", "[history]\nfull_sync_days = 30\n", "only used with"},
		{"bad mark_read", "[privacy]\nmark_read = \"sometimes\"\n", "privacy.mark_read must be"},
		{"old mark_read name", "[privacy]\nmark_read = \"everyone\"\n", "privacy.mark_read must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, Config{History: History{KeepDays: -5}}); err == nil {
		t.Fatal("Save accepted invalid config")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("invalid config was written (stat err = %v)", err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

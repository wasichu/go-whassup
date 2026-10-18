package app

import (
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// TestTUIDepsFullyWired fails if a ui.Deps field is left at its zero value
// by the real app (the UI's tests inject deps themselves, so they can't
// notice). Fields that may legitimately be empty are listed.
func TestTUIDepsFullyWired(t *testing.T) {
	t.Setenv("TMUX", "") // keep termcaps from asking a real tmux
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(t.Context(), filepath.Join(dir, "m.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck // test cleanup
	client, err := wa.Open(t.Context(), filepath.Join(dir, "s.db"), log, wa.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck // test cleanup
	deps := tuiDeps(config.Default(), paths.Paths{ConfigFile: filepath.Join(dir, "c.toml")}, client, st,
		&store.Recorder{Store: st, Log: log}, newEncryptor(t.Context(), st, paths.Paths{MediaDir: dir}, keySources{}, log), log)

	// Every field must be listed: a new ui.Deps field forces a decision
	// here instead of silently being left unset.
	required := map[string]bool{
		"Client": true, "Encryption": true, "Store": true, "Log": true, "Config": true, "ConfigPath": true,
		"ApplyConfig": true, "Media": true, "OpenFile": true,
	}
	mayBeEmpty := map[string]bool{
		"Graphics":  true, // depends on the terminal
		"FocusHint": true, // only set inside tmux with focus-events off
		"Notify":    true, // depends on the terminal
		"Notice":    true, // set by Run only while re-sealing
	}
	v := reflect.ValueOf(deps)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		switch {
		case required[name]:
			if v.Field(i).IsZero() {
				t.Errorf("ui.Deps.%s isn't wired by the app", name)
			}
		case !mayBeEmpty[name]:
			t.Errorf("ui.Deps.%s is new: wire it in tuiDeps and list it as required (or explain why it may be empty)", name)
		}
	}
}

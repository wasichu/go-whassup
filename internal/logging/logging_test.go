package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	for _, debug := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "wa.log")
		log, closer, err := Open(path, debug)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		log.Info("hello")
		log.Debug("chatter")
		if err := closer.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !strings.Contains(string(data), "hello") {
			t.Errorf("debug=%v: info line missing", debug)
		}
		if got := strings.Contains(string(data), "chatter"); got != debug {
			t.Errorf("debug=%v: debug line present = %v", debug, got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("log mode = %o, want 600", perm)
		}
	}
}

func TestCapChangesWhileRunning(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var c Cap
	capped := Capped(log, &c).With("lib", "whatsmeow")
	capped.Debug("first")
	c.Set(slog.LevelInfo)
	capped.Debug("hidden")
	capped.Info("shown")
	c.Clear()
	capped.Debug("again")
	got := buf.String()
	for _, want := range []string{"first", "shown", "again", "lib=whatsmeow"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "hidden") {
		t.Errorf("debug record passed the cap: %s", got)
	}
}

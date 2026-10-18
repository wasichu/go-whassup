package logging

import (
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

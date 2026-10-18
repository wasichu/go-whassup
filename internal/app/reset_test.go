package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wasichu/go-whassup/internal/paths"
)

func TestResetMessages(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{MediaDir: filepath.Join(dir, "media"), MessagesDB: filepath.Join(dir, "messages.db")}
	if err := os.MkdirAll(p.MediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.MessagesDB, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := resetMessages(p, strings.NewReader("no\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.MessagesDB); err != nil {
		t.Fatal("deleted without a yes")
	}
	out.Reset()
	if err := resetMessages(p, strings.NewReader("yes\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.MessagesDB); !os.IsNotExist(err) {
		t.Error("messages.db not deleted")
	}
	if _, err := os.Stat(p.MediaDir); !os.IsNotExist(err) {
		t.Error("media cache not deleted")
	}
	if !strings.Contains(out.String(), "is lost") {
		t.Errorf("warning missing: %s", out.String())
	}
}

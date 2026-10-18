package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesXDGEnv(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "/tmp/xdg-data/go-whassup"; p.DataDir != want {
		t.Errorf("DataDir = %q, want %q", p.DataDir, want)
	}
	if want := "/tmp/xdg-data/go-whassup/session.db"; p.SessionDB != want {
		t.Errorf("SessionDB = %q, want %q", p.SessionDB, want)
	}
	if want := "/tmp/xdg-state/go-whassup/wa.log"; p.LogFile != want {
		t.Errorf("LogFile = %q, want %q", p.LogFile, want)
	}
}

func TestResolveIgnoresRelativeXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "relative/path")
	t.Setenv("XDG_STATE_HOME", "")

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "go-whassup"); p.DataDir != want {
		t.Errorf("DataDir = %q, want %q", p.DataDir, want)
	}
	if want := filepath.Join(home, ".local", "state", "go-whassup"); p.StateDir != want {
		t.Errorf("StateDir = %q, want %q", p.StateDir, want)
	}
}

func TestEnsureCreatesDirs(t *testing.T) {
	root := t.TempDir()
	p := Paths{
		DataDir:  filepath.Join(root, "data", "go-whassup"),
		StateDir: filepath.Join(root, "state", "go-whassup"),
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, dir := range []string{p.DataDir, p.StateDir} {
		if !isDir(t, dir) {
			t.Errorf("%s was not created", dir)
		}
	}
}

func isDir(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

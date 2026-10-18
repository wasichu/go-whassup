// Package paths resolves where go-whassup keeps its runtime files.
//
// XDG locations are used on every platform (including macOS) so that data,
// logs and session state are easy to find and never end up in the repo.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const appName = "go-whassup"

// Paths holds the resolved runtime directories and files.
type Paths struct {
	ConfigDir  string // user-editable settings
	ConfigFile string
	DataDir    string // session + message databases
	StateDir   string // logs and other disposable state
	SessionDB  string // whatsmeow's store: device identity and Signal keys
	MessagesDB string // our chat/message store
	LogFile    string
}

// Resolve computes the runtime paths from the environment. It does not touch
// the filesystem; call Ensure to create the directories.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolving home directory: %w", err)
	}
	cfg := xdgDir("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	data := xdgDir("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	state := xdgDir("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	p := Paths{
		ConfigDir: filepath.Join(cfg, appName),
		DataDir:   filepath.Join(data, appName),
		StateDir:  filepath.Join(state, appName),
	}
	p.ConfigFile = filepath.Join(p.ConfigDir, "config.toml")
	p.SessionDB = filepath.Join(p.DataDir, "session.db")
	p.MessagesDB = filepath.Join(p.DataDir, "messages.db")
	p.LogFile = filepath.Join(p.StateDir, "wa.log")
	return p, nil
}

// Ensure creates the data and state directories with user-only
// permissions. The config directory is created only when saving config.
func (p Paths) Ensure() error {
	var errs []error
	for _, dir := range []string{p.DataDir, p.StateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			errs = append(errs, fmt.Errorf("creating %s: %w", dir, err))
		}
	}
	return errors.Join(errs...)
}

// xdgDir returns the env var's value if it is an absolute path, per the XDG
// spec, and fallback otherwise.
func xdgDir(env, fallback string) string {
	if v := os.Getenv(env); filepath.IsAbs(v) {
		return v
	}
	return fallback
}

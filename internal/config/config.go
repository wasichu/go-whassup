// Package config is the app's typed configuration, stored as TOML at
// paths.Paths.ConfigFile. The rest of the app receives a Config value and
// never reads the file itself. See docs/config.md.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the full configuration. Field comments are written into the
// file by Save and shown by the Settings screen, so keep them user-facing.
// Tag `applies:"pairing"` marks settings that only take effect when linking.
type Config struct {
	History    History    `toml:"history" comment:"How much chat history to fetch and keep."`
	UI         UI         `toml:"ui" comment:"How the terminal interface looks and behaves."`
	Privacy    Privacy    `toml:"privacy" comment:"What go-whassup tells WhatsApp about you."`
	Encryption Encryption `toml:"encryption" comment:"Encryption of stored messages at rest. See docs/storage.md."`
}

// Key sources for Encryption.KeySource.
const (
	KeySourceKeyring         = "keyring"
	KeySourcePasswordCommand = "password-command"
	KeySourcePrompt          = "prompt"
)

// Encryption controls at-rest encryption of messages.db.
type Encryption struct {
	Enabled         bool   `toml:"enabled" confirm:"Encrypts message text and attachment keys only: who you talk to, when, and contact names stay readable on disk. Turning it on re-encrypts stored messages and turns message search off; turning it off decrypts everything back. Takes effect when you next start go-whassup. Continue?" comment:"Encrypt message text and attachment keys in messages.db.\nEncrypts message text only: who you talk to, when, and contact names stay readable on disk.\nMessage search is unavailable while this is on."`
	KeySource       string `toml:"key_source" choices:"keyring,password-command,prompt" comment:"Where the key comes from: keyring = your OS keychain (no prompt);\npassword-command = run password_command and use its output as a passphrase;\nprompt = ask for a passphrase when go-whassup starts."`
	PasswordCommand string `toml:"password_command" text:"true" comment:"With key_source = password-command: a shell command that prints your passphrase,\ne.g. \"pass show whatsapp\" or \"gpg --decrypt ~/.config/go-whassup/pass.gpg\"."`
}

// Mark-read modes for Privacy.MarkRead.
const (
	MarkReadLocalOnly       = "local-only"        // nothing is sent
	MarkReadReceiptsOff     = "read-receipts-off" // your devices are told; no blue ticks
	MarkReadReceiptsOn      = "read-receipts-on"  // senders get read receipts (blue ticks)
	markReadChoicesForError = "local-only, read-receipts-off or read-receipts-on"
)

// Privacy controls what is sent on the user's behalf.
type Privacy struct {
	MarkRead string `toml:"mark_read" choices:"local-only,read-receipts-off,read-receipts-on" comment:"When you read a chat here: local-only = only clear it in go-whassup (nothing is sent);\nread-receipts-off = also mark it read on your phone and other devices, but senders see no blue ticks;\nread-receipts-on = also send read receipts (blue ticks), like the official apps."`
}

// UI controls the TUI.
type UI struct {
	InlineImages bool `toml:"inline_images" comment:"Show the login QR code as a small image in terminals that support the Kitty graphics protocol (Ghostty, kitty).\nfalse = always draw it with text blocks."`
}

// History controls history sync requests and local retention.
type History struct {
	RequestFullSync    bool `toml:"request_full_sync" applies:"pairing" comment:"Ask the phone for ~1 year of history when linking, instead of its default ~3 months.\nOnly takes effect when pairing a device (go-whassup login)."`
	FullSyncDays       int  `toml:"full_sync_days" applies:"pairing" comment:"With request_full_sync: how many days to ask for. 0 = the phone's full-sync default."`
	KeepDays           int  `toml:"keep_days" comment:"Delete stored messages older than this many days. 0 = keep everything."`
	MaxMessagesPerChat int  `toml:"max_messages_per_chat" comment:"Keep at most this many of the newest messages per chat. 0 = no limit."`
}

// Default returns the configuration used when no file exists: store
// everything the phone sends.
func Default() Config {
	return Config{
		UI:         UI{InlineImages: true},
		Privacy:    Privacy{MarkRead: MarkReadLocalOnly},
		Encryption: Encryption{KeySource: KeySourceKeyring},
	}
}

// Validate reports every invalid setting.
func (c Config) Validate() error {
	var errs []error
	nonNegative := func(name string, v int) {
		if v < 0 {
			errs = append(errs, fmt.Errorf("%s must be 0 or more, got %d", name, v))
		}
	}
	nonNegative("history.full_sync_days", c.History.FullSyncDays)
	nonNegative("history.keep_days", c.History.KeepDays)
	nonNegative("history.max_messages_per_chat", c.History.MaxMessagesPerChat)
	switch c.Privacy.MarkRead {
	case MarkReadLocalOnly, MarkReadReceiptsOff, MarkReadReceiptsOn:
	default:
		errs = append(errs, fmt.Errorf("privacy.mark_read must be %s, got %q", markReadChoicesForError, c.Privacy.MarkRead))
	}
	switch c.Encryption.KeySource {
	case KeySourceKeyring, KeySourcePasswordCommand, KeySourcePrompt:
	default:
		errs = append(errs, fmt.Errorf("encryption.key_source must be keyring, password-command or prompt, got %q", c.Encryption.KeySource))
	}
	if c.Encryption.Enabled && c.Encryption.KeySource == KeySourcePasswordCommand && strings.TrimSpace(c.Encryption.PasswordCommand) == "" {
		errs = append(errs, errors.New("encryption.password_command is required with key_source = password-command"))
	}
	if c.History.FullSyncDays > 0 && !c.History.RequestFullSync {
		errs = append(errs, errors.New("history.full_sync_days is only used with history.request_full_sync = true"))
	}
	return errors.Join(errs...)
}

// Load reads the config at path, layered over Default. A missing file is not
// an error. Unknown keys are rejected so typos don't silently do nothing.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}

	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return cfg, fmt.Errorf("config %s: unknown settings:\n%s", path, strict.String())
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			return cfg, fmt.Errorf("config %s:%d:%d: %w", path, row, col, err)
		}
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

const header = `# go-whassup configuration. See docs/config.md.
# This file is rewritten by the TUI's Settings screen: comments you add will be lost.

`

// Save validates cfg and writes it to path atomically (temp file + rename),
// readable only by the user.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	body, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	// Best effort: after a successful rename the temp name no longer exists.
	defer os.Remove(tmp.Name())

	_, werr := tmp.Write(append([]byte(header), body...))
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	// CreateTemp uses 0600 already; Chmod guards against an unusual umask.
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("securing config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing config: %w", err)
	}
	return nil
}

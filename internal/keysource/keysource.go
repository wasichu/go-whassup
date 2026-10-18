// Package keysource gets the secret that protects the message key: a random
// key kept in the OS keyring, or a passphrase from a command or a prompt.
// Secrets never appear on command lines (other users can see those).
package keysource

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// ErrNotFound means the keyring has no secret for us yet.
var ErrNotFound = errors.New("no key in the keyring")

// runner runs a command with stdin and returns stdout; swapped in tests.
type runner func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

func run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s didn't respond in time (is the keychain locked, or waiting for approval in a dialog?)", name)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out, fmt.Errorf("%s: exit %d: %s", name, exit.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// Keyring stores a random key-encryption key in the OS keyring: the macOS
// Keychain (security) or the Secret Service on Linux (secret-tool).
type Keyring struct {
	Service, Account string
	goos             string // runtime.GOOS unless set (tests)
	run              runner
}

// NewKeyring returns the keyring entry for go-whassup's message key.
func NewKeyring() Keyring {
	return Keyring{Service: "go-whassup", Account: "messages-key"}
}

func (k Keyring) os() string {
	if k.goos != "" {
		return k.goos
	}
	return runtime.GOOS
}

func (k Keyring) runner() runner {
	if k.run != nil {
		return k.run
	}
	return run
}

// validName keeps Service and Account safe to place in security -i's
// whitespace-split command line.
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (k Keyring) check() error {
	if !validName.MatchString(k.Service) || !validName.MatchString(k.Account) {
		return fmt.Errorf("invalid keyring service/account %q/%q", k.Service, k.Account)
	}
	return nil
}

// Get returns the stored key, or ErrNotFound. The key is random, so it is
// used directly as the key-encryption key (no passphrase derivation).
func (k Keyring) Get(ctx context.Context) ([]byte, error) {
	if err := k.check(); err != nil {
		return nil, err
	}
	var out []byte
	var err error
	switch k.os() {
	case "darwin":
		out, err = k.runner()(ctx, nil, "security", "find-generic-password", "-a", k.Account, "-s", k.Service, "-w")
		if err != nil && strings.Contains(err.Error(), "exit 44") {
			return nil, ErrNotFound
		}
	case "linux", "freebsd", "openbsd":
		out, err = k.runner()(ctx, nil, "secret-tool", "lookup", "service", k.Service, "account", k.Account)
		if err == nil && len(bytes.TrimSpace(out)) == 0 {
			return nil, ErrNotFound
		}
		if err != nil && strings.Contains(err.Error(), "exit 1") {
			return nil, ErrNotFound
		}
	default:
		return nil, fmt.Errorf("no keyring support on %s; use key_source = password-command or prompt", k.os())
	}
	if err != nil {
		return nil, fmt.Errorf("reading keyring: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("keyring entry %s/%s isn't a go-whassup key", k.Service, k.Account)
	}
	return key, nil
}

// Put stores key (replacing any existing entry), passing it on stdin.
func (k Keyring) Put(ctx context.Context, key []byte) error {
	if err := k.check(); err != nil {
		return err
	}
	secret := hex.EncodeToString(key)
	var err error
	switch k.os() {
	case "darwin":
		// security -i reads commands from stdin, keeping the secret off argv.
		cmd := fmt.Sprintf("add-generic-password -U -a %s -s %s -l %s -w %s\n", k.Account, k.Service, k.Service, secret)
		_, err = k.runner()(ctx, []byte(cmd), "security", "-i")
	case "linux", "freebsd", "openbsd":
		_, err = k.runner()(ctx, []byte(secret), "secret-tool", "store", "--label=go-whassup message key",
			"service", k.Service, "account", k.Account)
	default:
		return fmt.Errorf("no keyring support on %s; use key_source = password-command or prompt", k.os())
	}
	if err != nil {
		return fmt.Errorf("writing keyring: %w", err)
	}
	return nil
}

// Delete removes the stored key, if any.
func (k Keyring) Delete(ctx context.Context) error {
	if err := k.check(); err != nil {
		return err
	}
	var err error
	switch k.os() {
	case "darwin":
		_, err = k.runner()(ctx, nil, "security", "delete-generic-password", "-a", k.Account, "-s", k.Service)
		if err != nil && strings.Contains(err.Error(), "exit 44") {
			return nil
		}
	case "linux", "freebsd", "openbsd":
		_, err = k.runner()(ctx, nil, "secret-tool", "clear", "service", k.Service, "account", k.Account)
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting keyring entry: %w", err)
	}
	return nil
}

// Command runs the user's password_command and uses its output as the
// passphrase.
type Command struct {
	Cmd        string
	ConfigPath string // the file Cmd came from; must not be writable by others
	run        runner
}

// Passphrase runs the command. Because the command comes from the config
// file, a config others can write would let them run anything as you, so
// it's refused.
func (c Command) Passphrase(ctx context.Context) ([]byte, error) {
	if strings.TrimSpace(c.Cmd) == "" {
		return nil, errors.New("encryption.password_command is empty")
	}
	// The file, and its directory (someone could replace the file), must
	// not be writable by others.
	for _, p := range []string{c.ConfigPath, filepath.Dir(c.ConfigPath)} {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("checking %s: %w", p, err)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return nil, fmt.Errorf("refusing to run password_command: %s is writable by other users (chmod go-w it)", p)
		}
	}
	r := c.run
	if r == nil {
		r = run
	}
	out, err := r(ctx, []byte{}, "sh", "-c", c.Cmd)
	if err != nil {
		return nil, fmt.Errorf("password_command failed: %w", err)
	}
	// Strip exactly one line ending: a passphrase may end in spaces.
	out = bytes.TrimSuffix(out, []byte("\n"))
	out = bytes.TrimSuffix(out, []byte("\r"))
	if len(out) == 0 {
		return nil, errors.New("password_command printed nothing")
	}
	return out, nil
}

// Prompt asks for a passphrase on the terminal without echoing it.
type Prompt struct {
	In  *os.File  // usually os.Stdin
	Out io.Writer // usually os.Stderr
}

// ErrNoTerminal means a prompt was needed but there's no terminal to ask on.
var ErrNoTerminal = errors.New("a passphrase prompt needs a terminal; use key_source = keyring or password-command")

// Passphrase asks once. confirm asks twice and requires a match (for
// setting a new passphrase).
func (p Prompt) Passphrase(label string, confirm bool) ([]byte, error) {
	if p.In == nil || !term.IsTerminal(p.In.Fd()) {
		return nil, ErrNoTerminal
	}
	ask := func(l string) ([]byte, error) {
		if _, err := fmt.Fprint(p.Out, l); err != nil {
			return nil, err
		}
		pw, err := term.ReadPassword(p.In.Fd())
		if _, nerr := fmt.Fprintln(p.Out); nerr != nil && err == nil {
			err = nerr
		}
		return pw, err
	}
	pw, err := ask(label)
	if err != nil {
		return nil, fmt.Errorf("reading passphrase: %w", err)
	}
	if len(pw) == 0 {
		return nil, errors.New("empty passphrase")
	}
	if confirm {
		again, err := ask("Again: ")
		if err != nil {
			return nil, fmt.Errorf("reading passphrase: %w", err)
		}
		if !bytes.Equal(pw, again) {
			return nil, errors.New("passphrases don't match")
		}
	}
	return pw, nil
}

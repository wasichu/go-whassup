package keysource

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	stdin string
	argv  []string
}

func recorder(out string, err error, calls *[]call) runner {
	return func(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{stdin: string(stdin), argv: append([]string{name}, args...)})
		return []byte(out), err
	}
}

func TestKeyringDarwinKeepsSecretOffArgv(t *testing.T) {
	var calls []call
	k := Keyring{Service: "svc", Account: "acct", goos: "darwin", run: recorder("", nil, &calls)}
	if err := k.Put(t.Context(), []byte{0xAB, 0xCD}); err != nil {
		t.Fatal(err)
	}
	c := calls[0]
	if strings.Join(c.argv, " ") != "security -i" {
		t.Errorf("argv = %v; the secret must not be on the command line", c.argv)
	}
	if !strings.Contains(c.stdin, "-w abcd") || !strings.Contains(c.stdin, "-U -a acct -s svc") {
		t.Errorf("stdin = %q", c.stdin)
	}
	for _, a := range c.argv {
		if strings.Contains(a, "abcd") {
			t.Fatal("secret on argv")
		}
	}

	calls = nil
	k.run = recorder("abcd\n", nil, &calls)
	got, err := k.Get(t.Context())
	if err != nil || !bytes.Equal(got, []byte{0xAB, 0xCD}) {
		t.Errorf("Get = %x, %v", got, err)
	}

	k.run = recorder("", errors.New("security: exit 44: The specified item could not be found"), &calls)
	if _, err := k.Get(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item = %v, want ErrNotFound", err)
	}
	if err := k.Delete(t.Context()); err != nil {
		t.Errorf("deleting a missing item should be fine: %v", err)
	}
	k.run = recorder("not hex", nil, &calls)
	if _, err := k.Get(t.Context()); err == nil {
		t.Error("garbage keyring entry accepted")
	}
}

func TestKeyringLinuxUsesStdin(t *testing.T) {
	var calls []call
	k := Keyring{Service: "svc", Account: "acct", goos: "linux", run: recorder("", nil, &calls)}
	if err := k.Put(t.Context(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	if calls[0].argv[0] != "secret-tool" || calls[0].stdin != "01" {
		t.Errorf("call = %+v", calls[0])
	}
	if _, err := k.Get(t.Context()); !errors.Is(err, ErrNotFound) { // empty lookup output
		t.Errorf("empty lookup = %v, want ErrNotFound", err)
	}
	if err := (Keyring{Service: "svc x", Account: "a", goos: "darwin", run: recorder("", nil, &calls)}).Put(t.Context(), []byte{1}); err == nil {
		t.Error("a service name with a space would change the keychain entry; must be refused")
	}
	if _, err := (Keyring{Service: "s", Account: "a", goos: "plan9"}).Get(t.Context()); err == nil || !strings.Contains(err.Error(), "password-command") {
		t.Errorf("unsupported OS = %v", err)
	}
}

func configFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommand(t *testing.T) {
	var calls []call
	c := Command{Cmd: "pass show whatsapp", ConfigPath: configFile(t, 0o600), run: recorder("pw with space \n", nil, &calls)}
	got, err := c.Passphrase(t.Context())
	if err != nil || string(got) != "pw with space " {
		t.Fatalf("Passphrase = %q, %v; one newline stripped, trailing space kept", got, err)
	}
	if strings.Join(calls[0].argv, " ") != "sh -c pass show whatsapp" {
		t.Errorf("argv = %v", calls[0].argv)
	}

	c.ConfigPath = configFile(t, 0o664)
	if _, err := c.Passphrase(t.Context()); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Errorf("group-writable config = %v", err)
	}
	c.ConfigPath = configFile(t, 0o600)
	if err := os.Chmod(filepath.Dir(c.ConfigPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Passphrase(t.Context()); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Errorf("world-writable config dir = %v", err)
	}
	c.ConfigPath = configFile(t, 0o600)
	c.run = recorder("", nil, &calls)
	if _, err := c.Passphrase(t.Context()); err == nil {
		t.Error("empty output accepted")
	}
	c.Cmd = ""
	if _, err := c.Passphrase(t.Context()); err == nil {
		t.Error("empty command accepted")
	}
}

func TestCommandRealShell(t *testing.T) {
	c := Command{Cmd: "printf 'secret\\n'", ConfigPath: configFile(t, 0o600)}
	got, err := c.Passphrase(context.Background())
	if err != nil || string(got) != "secret" {
		t.Errorf("real sh = %q, %v", got, err)
	}
	c.Cmd = "echo oops >&2; exit 3"
	if _, err := c.Passphrase(context.Background()); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Errorf("failing command should report its stderr: %v", err)
	}
}

func TestPromptNeedsTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := (Prompt{In: r, Out: &bytes.Buffer{}}).Passphrase("Passphrase: ", false); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("pipe = %v, want ErrNoTerminal", err)
	}
}

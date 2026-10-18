package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/keysource"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

type fakeKeyring struct{ key []byte }

func (f *fakeKeyring) Get(context.Context) ([]byte, error) {
	if f.key == nil {
		return nil, keysource.ErrNotFound
	}
	return f.key, nil
}
func (f *fakeKeyring) Put(_ context.Context, k []byte) error { f.key = k; return nil }

func fakeSources(kr *fakeKeyring, pass string) keySources {
	return keySources{keyring: kr, passphrase: func(context.Context, string, bool) ([]byte, error) {
		return []byte(pass), nil
	}}
}

type encFixture struct {
	t    *testing.T
	p    paths.Paths
	path string
	log  *slog.Logger
}

func newEncFixture(t *testing.T) *encFixture {
	dir := t.TempDir()
	f := &encFixture{t: t, path: filepath.Join(dir, "messages.db"), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		p: paths.Paths{MediaDir: filepath.Join(dir, "media"), MessagesDB: filepath.Join(dir, "messages.db")}}
	st := f.open()
	if _, err := st.SaveMessages(t.Context(), []wa.Message{
		{ID: "1", Chat: "a@s.whatsapp.net", Sender: "a@s.whatsapp.net", Timestamp: time.Unix(1, 0), Kind: wa.KindText, Text: "lunch plans"},
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *encFixture) open() *store.Store {
	f.t.Helper()
	st, err := store.Open(f.t.Context(), f.path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck // test cleanup
	return st
}

func cfgWith(enabled bool, source string) config.Config {
	c := config.Default()
	c.Encryption.Enabled, c.Encryption.KeySource = enabled, source
	return c
}

// crypto returns an encryptor for st, closed when the test ends (before
// the store: cleanups run in reverse).
func (f *encFixture) crypto(st *store.Store, ks keySources) *encryptor {
	f.t.Helper()
	c := newEncryptor(f.t.Context(), st, f.p, ks, f.log)
	f.t.Cleanup(func() {
		if err := c.Close(); err != nil {
			f.t.Errorf("Close: %v", err)
		}
	})
	return c
}

// run starts encryption as the app does and lets the re-seal finish.
func (f *encFixture) run(st *store.Store, cfg config.Config, ks keySources, transitions bool) (encryption, error) {
	f.t.Helper()
	c := f.crypto(st, ks)
	e, err := c.Start(f.t.Context(), cfg, transitions)
	if done := c.Running(); err == nil && done != nil {
		if derr := <-done; derr != nil {
			f.t.Fatalf("re-seal: %v", derr)
		}
	}
	return e, err
}

func TestEnableWithKeyringThenUnlockOnRestart(t *testing.T) {
	f := newEncFixture(t)
	if err := os.MkdirAll(f.p.MediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	kr := &fakeKeyring{}
	st := f.open()
	e, err := f.run(st, cfgWith(true, config.KeySourceKeyring), fakeSources(kr, ""), true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !e.on || e.finish == nil || kr.key == nil {
		t.Fatalf("enable state = %+v, keyring set = %v", e, kr.key != nil)
	}
	if _, err := os.Stat(f.p.MediaDir); !os.IsNotExist(err) {
		t.Error("plaintext media cache must be removed when enabling")
	}
	if _, err := st.Search(t.Context(), "lunch", 5); !errors.Is(err, store.ErrSearchUnavailable) {
		t.Errorf("search after enabling = %v", err)
	}
	if n, err := st.Pending(t.Context()); err != nil || n != 0 {
		t.Errorf("pending after re-seal = %d, %v", n, err)
	}

	// Restart: a fresh store unlocks from the keyring and reads.
	again := f.open()
	if _, err := f.run(again, cfgWith(true, config.KeySourceKeyring), fakeSources(kr, ""), true); err != nil {
		t.Fatalf("unlock on restart: %v", err)
	}
	msgs, err := again.Messages(t.Context(), "a@s.whatsapp.net", 0)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "lunch plans" {
		t.Errorf("read after restart = %+v, %v", msgs, err)
	}
}

func TestPassphraseSourcesAndWrongPassphrase(t *testing.T) {
	f := newEncFixture(t)
	st := f.open()
	if _, err := f.run(st, cfgWith(true, config.KeySourcePrompt), fakeSources(&fakeKeyring{}, "correct horse"), true); err != nil {
		t.Fatalf("enable with prompt: %v", err)
	}
	_, err := f.run(f.open(), cfgWith(true, config.KeySourcePrompt), fakeSources(&fakeKeyring{}, "wrong"), true)
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "wrong passphrase") || !strings.Contains(err.Error(), "db reset") {
		t.Errorf("wrong passphrase = %v", err)
	}
	if _, err := f.run(f.open(), cfgWith(true, config.KeySourcePrompt), fakeSources(&fakeKeyring{}, "correct horse"), true); err != nil {
		t.Errorf("right passphrase = %v", err)
	}
}

func TestMissingKeychainEntry(t *testing.T) {
	f := newEncFixture(t)
	if _, err := f.run(f.open(), cfgWith(true, config.KeySourceKeyring), fakeSources(&fakeKeyring{}, ""), true); err != nil {
		t.Fatal(err)
	}
	_, err := f.run(f.open(), cfgWith(true, config.KeySourceKeyring), fakeSources(&fakeKeyring{}, ""), true)
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "keychain entry is missing") {
		t.Errorf("missing keychain entry = %v", err)
	}
}

func TestDisableDecryptsBack(t *testing.T) {
	f := newEncFixture(t)
	kr := &fakeKeyring{}
	if _, err := f.run(f.open(), cfgWith(true, config.KeySourceKeyring), fakeSources(kr, ""), true); err != nil {
		t.Fatal(err)
	}
	st := f.open()
	e, err := f.run(st, cfgWith(false, config.KeySourceKeyring), fakeSources(kr, ""), true)
	if err != nil || e.finish == nil || !e.on {
		t.Fatalf("disable = %+v, %v (on until decrypted)", e, err)
	}
	if k, err := st.Key(t.Context()); err != nil || k != nil {
		t.Errorf("key after decrypting = %+v, %v", k, err)
	}
	hits, err := st.Search(t.Context(), "lunch", 5)
	if err != nil || len(hits) != 1 {
		t.Errorf("search after decrypting = %v, %v", hits, err)
	}
	// Next start: nothing to do.
	e, err = f.run(f.open(), cfgWith(false, config.KeySourceKeyring), fakeSources(kr, ""), true)
	if err != nil || e.on || e.finish != nil {
		t.Errorf("after decrypting = %+v, %v", e, err)
	}
}

func TestDBCommandsOnlyUnlock(t *testing.T) {
	f := newEncFixture(t)
	kr := &fakeKeyring{}
	if _, err := f.run(f.open(), cfgWith(true, config.KeySourceKeyring), fakeSources(kr, ""), true); err != nil {
		t.Fatal(err)
	}
	// Config says off, but a db command must not start decrypting.
	st := f.open()
	e, err := f.run(st, cfgWith(false, config.KeySourceKeyring), fakeSources(kr, ""), false)
	if err != nil || e.finish != nil || !e.on {
		t.Fatalf("db command = %+v, %v", e, err)
	}
	if k, err := st.Key(t.Context()); err != nil || k == nil {
		t.Error("db command removed the key")
	}
	// And with no key and encryption on in the config, it doesn't enable.
	g := newEncFixture(t)
	e, err = g.run(g.open(), cfgWith(true, config.KeySourceKeyring), fakeSources(&fakeKeyring{}, ""), false)
	if err != nil || e.finish != nil {
		t.Errorf("db command enabling = %+v, %v", e, err)
	}
}

func TestResetMessages(t *testing.T) {
	f := newEncFixture(t)
	if err := os.MkdirAll(f.p.MediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := resetMessages(f.p, strings.NewReader("no\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.path); err != nil {
		t.Fatal("deleted without a yes")
	}
	out.Reset()
	if err := resetMessages(f.p, strings.NewReader("yes\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.path); !os.IsNotExist(err) {
		t.Error("messages.db not deleted")
	}
	if _, err := os.Stat(f.p.MediaDir); !os.IsNotExist(err) {
		t.Error("media cache not deleted")
	}
	if !strings.Contains(out.String(), "is lost") {
		t.Errorf("warning missing: %s", out.String())
	}
}

func TestCrashBeforeFinishIsCompletedNextStart(t *testing.T) {
	f := newEncFixture(t)
	kr := &fakeKeyring{}
	st := f.open()
	if _, err := f.crypto(st, fakeSources(kr, "")).setUp(t.Context(), cfgWith(true, config.KeySourceKeyring), true); err != nil {
		t.Fatal(err)
	}
	// Re-seal everything, then "crash" before FinishEncrypting.
	for {
		n, err := st.ResealBatch(t.Context(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	again := f.open()
	c := f.crypto(again, fakeSources(kr, ""))
	e, err := c.setUp(t.Context(), cfgWith(true, config.KeySourceKeyring), true)
	if err != nil || e.pending != 0 || e.finish == nil {
		t.Fatalf("restart after a crash before finishing = %+v, %v; want the finish step to run", e, err)
	}
	if err := c.reseal(t.Context(), e.finish, e.pending); err != nil {
		t.Fatal(err)
	}
	k, err := again.Key(t.Context())
	if err != nil || !k.Finished {
		t.Errorf("key = %+v, %v; want Finished", k, err)
	}
	e, err = f.crypto(f.open(), fakeSources(kr, "")).setUp(t.Context(), cfgWith(true, config.KeySourceKeyring), true)
	if err != nil || e.finish != nil {
		t.Errorf("once finished, nothing more to do: %+v, %v", e, err)
	}
}

// wait returns a done channel's outcome, failing the test if it takes long.
func wait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("re-seal didn't finish")
		return nil
	}
}

func TestLiveEncryptAndDecrypt(t *testing.T) {
	f := newEncFixture(t)
	kr := &fakeKeyring{}
	st := f.open()
	c := f.crypto(st, fakeSources(kr, ""))
	if _, err := c.Start(t.Context(), cfgWith(false, config.KeySourceKeyring), true); err != nil {
		t.Fatal(err)
	}
	if c.cache.Dir() != f.p.MediaDir {
		t.Fatalf("unencrypted: cache in %s", c.cache.Dir())
	}

	done, err := c.Set(t.Context(), true, config.KeySourceKeyring, nil)
	if err != nil {
		t.Fatalf("turning on: %v", err)
	}
	if err := wait(t, done); err != nil {
		t.Fatalf("encrypting: %v", err)
	}
	k, err := st.Key(t.Context())
	if err != nil || k == nil || !k.Finished || kr.key == nil {
		t.Fatalf("after encrypting: key %+v, %v, keyring set %v", k, err, kr.key != nil)
	}
	if _, err := st.Search(t.Context(), "lunch", 5); !errors.Is(err, store.ErrSearchUnavailable) {
		t.Errorf("search while encrypted = %v", err)
	}
	if c.cache.Dir() == f.p.MediaDir {
		t.Error("downloads must move to a temporary directory while encrypted")
	}

	done, err = c.Set(t.Context(), false, config.KeySourceKeyring, nil)
	if err != nil {
		t.Fatalf("turning off: %v", err)
	}
	if err := wait(t, done); err != nil {
		t.Fatalf("decrypting: %v", err)
	}
	if k, err := st.Key(t.Context()); err != nil || k != nil {
		t.Errorf("key after decrypting = %+v, %v; want none", k, err)
	}
	if hits, err := st.Search(t.Context(), "lunch", 5); err != nil || len(hits) != 1 {
		t.Errorf("search after decrypting = %v, %v", hits, err)
	}
	if c.cache.Dir() != f.p.MediaDir {
		t.Errorf("after decrypting, downloads go back to %s, not %s", f.p.MediaDir, c.cache.Dir())
	}
}

func TestReenableWhileDecryptingReusesTheKey(t *testing.T) {
	f := newEncFixture(t)
	kr := &fakeKeyring{}
	st := f.open()
	c := f.crypto(st, fakeSources(kr, ""))
	if _, err := c.Start(t.Context(), cfgWith(false, config.KeySourceKeyring), true); err != nil {
		t.Fatal(err)
	}
	done, err := c.Set(t.Context(), true, config.KeySourceKeyring, nil)
	if err == nil {
		err = wait(t, done)
	}
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.Key(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// Decrypting is held before its first batch until it's replaced.
	held := make(chan struct{})
	var once sync.Once
	c.beforeBatch = func(ctx context.Context) {
		once.Do(func() {
			close(held)
			<-ctx.Done()
		})
	}
	decrypting, err := c.Set(t.Context(), false, config.KeySourceKeyring, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-held
	done, err = c.Set(t.Context(), true, config.KeySourceKeyring, nil)
	if err != nil {
		t.Fatalf("turning on again: %v", err)
	}
	if err := wait(t, decrypting); !errors.Is(err, errReplaced) {
		t.Errorf("replaced decrypt ended with %v, want errReplaced", err)
	}
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	k, err := st.Key(t.Context())
	if err != nil || k == nil || k.ID != first.ID || !k.Finished {
		t.Errorf("key = %+v, %v; want the same key (%d), finished", k, err, first.ID)
	}
	if n, err := st.Pending(t.Context()); err != nil || n != 0 {
		t.Errorf("pending = %d, %v", n, err)
	}
}

func TestLivePromptSource(t *testing.T) {
	f := newEncFixture(t)
	st := f.open()
	c := f.crypto(st, fakeSources(&fakeKeyring{}, "unused"))
	if _, err := c.Start(t.Context(), cfgWith(false, config.KeySourcePrompt), true); err != nil {
		t.Fatal(err)
	}
	if need, err := c.NeedsPassphrase(t.Context(), true, config.KeySourcePrompt); err != nil || !need {
		t.Errorf("NeedsPassphrase = %v, %v; want true", need, err)
	}
	if _, err := c.Set(t.Context(), true, config.KeySourcePrompt, nil); err == nil {
		t.Fatal("prompt without a passphrase should fail")
	}
	if k, _ := st.Key(t.Context()); k != nil { //nolint:errcheck // checked by the next Set
		t.Fatal("a failed enable left a key")
	}
	pass := []byte("correct horse")
	done, err := c.Set(t.Context(), true, config.KeySourcePrompt, pass)
	if err == nil {
		err = wait(t, done)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pass, make([]byte, len(pass))) {
		t.Error("the passphrase should be zeroed after use")
	}
	if need, _ := c.NeedsPassphrase(t.Context(), false, config.KeySourcePrompt); need { //nolint:errcheck // false either way
		t.Error("turning off never needs a passphrase")
	}

	// Restart: the passphrase typed in Settings unlocks it.
	again := f.open()
	if _, err := f.run(again, cfgWith(true, config.KeySourcePrompt), fakeSources(nil, "correct horse"), true); err != nil {
		t.Fatalf("unlock with the passphrase set live: %v", err)
	}
}

package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/store"
)

type encCall struct {
	on     bool
	source string
	pass   string
}

// fakeEncryption records Set calls; each re-seal finishes at once.
type fakeEncryption struct {
	mu    sync.Mutex
	calls []encCall
	needs bool
	err   error
}

func (f *fakeEncryption) Set(_ context.Context, on bool, source string, pass []byte) (<-chan error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, encCall{on, source, string(pass)})
	if f.err != nil {
		return nil, f.err
	}
	done := make(chan error, 1)
	done <- nil
	return done, nil
}

func (f *fakeEncryption) NeedsPassphrase(_ context.Context, on bool, source string) (bool, error) {
	return f.needs && on && source == config.KeySourcePrompt, nil
}

func (f *fakeEncryption) Running() <-chan error { return nil }

func (f *fakeEncryption) got() []encCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]encCall(nil), f.calls...)
}

// withEncryption gives the harness a fake live encryption and key source.
func (h *harness) withEncryption(f *fakeEncryption, source string) {
	m := h.model()
	m.deps.Encryption = f
	m.deps.Config.Encryption.KeySource = source
	h.m = m
}

// toggleEncryption opens Settings and answers the encryption question.
func (h *harness) toggleEncryption(answer string) {
	h.t.Helper()
	h.key(",")
	for range 6 { // down to encryption.enabled
		h.key("down")
	}
	h.key("enter")
	h.key(answer)
	h.settle()
}

func TestEncryptionTurnsOnLive(t *testing.T) {
	h := newHarness(t, true)
	enc := &fakeEncryption{}
	h.withEncryption(enc, config.KeySourceKeyring)
	h.toggleEncryption("y")
	h.waitSaves(1)
	h.waitFor("the finish notice", func() bool { return strings.Contains(h.model().notice.text, "Encryption is on") })
	if got := enc.got(); len(got) != 1 || !got[0].on || got[0].source != config.KeySourceKeyring {
		t.Fatalf("Set calls = %+v", got)
	}
	if len(h.applied) != 1 || !h.applied[0].Encryption.Enabled || !h.model().deps.Config.Encryption.Enabled {
		t.Errorf("config not saved after turning on: %+v", h.applied)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Encryption is on") {
		t.Errorf("finish not reported:\n%s", v)
	}

	h.key("enter") // and off again: no passphrase, decrypts
	h.key("y")
	h.waitSaves(2)
	h.waitFor("the decrypt notice", func() bool { return strings.Contains(h.model().notice.text, "search is back") })
	if got := enc.got(); len(got) != 2 || got[1].on {
		t.Fatalf("Set calls = %+v", got)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "search is back") {
		t.Errorf("decrypt not reported:\n%s", v)
	}
}

func TestEncryptionAsksForPassphrase(t *testing.T) {
	h := newHarness(t, true)
	enc := &fakeEncryption{needs: true}
	h.withEncryption(enc, config.KeySourcePrompt)
	h.toggleEncryption("y")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Passphrase for stored messages") || len(enc.got()) != 0 {
		t.Fatalf("passphrase not asked first (calls %+v):\n%s", enc.got(), v)
	}
	h.key("enter") // empty
	if v := ansi.Strip(h.view()); !strings.Contains(v, "can't be empty") {
		t.Errorf("empty passphrase accepted:\n%s", v)
	}
	h.key("secret")
	if v := ansi.Strip(h.view()); strings.Contains(v, "secret") {
		t.Errorf("passphrase shown in clear:\n%s", v)
	}
	h.key("enter")
	h.key("other")
	h.key("enter")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "didn't match") || len(enc.got()) != 0 {
		t.Fatalf("mismatch not caught:\n%s", v)
	}
	h.key("secret")
	h.key("enter")
	h.key("secret")
	h.key("enter")
	h.waitSaves(1)
	if got := enc.got(); len(got) != 1 || got[0].pass != "secret" || !got[0].on {
		t.Fatalf("Set calls = %+v", got)
	}
	if !h.model().deps.Config.Encryption.Enabled {
		t.Error("config not saved")
	}
}

func TestEncryptionPassphraseEscCancels(t *testing.T) {
	h := newHarness(t, true)
	enc := &fakeEncryption{needs: true}
	h.withEncryption(enc, config.KeySourcePrompt)
	h.toggleEncryption("y")
	h.key("esc")
	h.settle()
	if len(enc.got()) != 0 || len(h.applied) != 0 || h.model().settings.cfg.Encryption.Enabled {
		t.Errorf("esc should drop the change: calls %+v applied %+v", enc.got(), h.applied)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Settings") {
		t.Errorf("should be back on Settings:\n%s", v)
	}
}

func TestEncryptionFailureLeavesConfig(t *testing.T) {
	h := newHarness(t, true)
	enc := &fakeEncryption{err: errors.New("keychain access denied")}
	h.withEncryption(enc, config.KeySourceKeyring)
	h.toggleEncryption("y")
	h.waitSaves(1) // the failure arrives as a save that didn't happen
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "turning encryption on: keychain access denied") {
		t.Errorf("error not shown:\n%s", v)
	}
	if len(h.applied) != 0 || h.model().deps.Config.Encryption.Enabled || h.model().settings.cfg.Encryption.Enabled {
		t.Error("a failed change must not be saved")
	}
}

func TestSearchComesBackWhenDecrypted(t *testing.T) {
	h := newHarness(t, true)
	plain := h.model().deps.Store
	m := h.model()
	m.deps.Store = encryptedStore{plain}
	h.m = m
	h.key("s")
	h.settle()
	if !h.model().search.off {
		t.Fatal("search should be off while encrypted")
	}
	m = h.model()
	m.deps.Store = plain // decrypting finished
	h.m = m
	h.send(encryptionDoneMsg{seq: h.model().encSeq, on: false})
	h.settle()
	if s := h.model().search; s.off || s.err != "" {
		t.Errorf("search still off after decrypting: %+v", s.err)
	}
	h.key("bob")
	h.settle()
	if v := ansi.Strip(h.view()); !strings.Contains(v, "hi from bob") {
		t.Errorf("search doesn't work after decrypting:\n%s", v)
	}
}

func TestEncryptionFailureWording(t *testing.T) {
	h := newHarness(t, true)
	h.send(encryptionDoneMsg{seq: h.model().encSeq, err: &store.ResealError{Chat: "c", ID: "m1", Err: errors.New("message authentication failed")}})
	if n := h.model().notice.text; !strings.Contains(n, "stops there every time") || strings.Contains(n, "retried") {
		t.Errorf("unreadable value: %q", n)
	}
	h.send(encryptionDoneMsg{seq: h.model().encSeq, err: errors.New("disk I/O error")})
	if n := h.model().notice.text; !strings.Contains(n, "retried at the next start") {
		t.Errorf("other failure: %q", n)
	}
}

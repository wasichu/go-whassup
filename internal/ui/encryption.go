package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/store"
)

// Encryption turns at-rest encryption on and off while the app runs
// (implemented by internal/encryption.Controller). Re-sealing stored messages happens in the
// background; a done channel reports how it ended.
type Encryption interface {
	// Set starts turning encryption on or off. pass is a new passphrase
	// for key source "prompt" (see NeedsPassphrase); it's zeroed.
	Set(ctx context.Context, on bool, source string, pass []byte) (done <-chan error, err error)
	// NeedsPassphrase reports whether Set needs a passphrase from the user.
	NeedsPassphrase(ctx context.Context, on bool, source string) (bool, error)
	// Running is a re-seal started at launch, if any.
	Running() <-chan error
}

type (
	// passphraseCheckedMsg says whether turning encryption on needs a
	// passphrase typed here.
	passphraseCheckedMsg struct {
		cfg    config.Config
		needed bool
		err    error
	}
	// encryptionDoneMsg reports the end of a background re-seal.
	encryptionDoneMsg struct {
		seq int
		on  bool
		err error
	}
)

// encryptionChanged reports whether next turns encryption on or off.
func encryptionChanged(cur, next config.Config) bool {
	return cur.Encryption.Enabled != next.Encryption.Enabled
}

func checkPassphrase(ctx context.Context, enc Encryption, next config.Config) tea.Cmd {
	return func() tea.Msg {
		needed, err := enc.NeedsPassphrase(ctx, next.Encryption.Enabled, next.Encryption.KeySource)
		return passphraseCheckedMsg{cfg: next, needed: needed, err: err}
	}
}

// applyEncryption turns encryption on or off and, only if that worked,
// saves the config: a failure (keychain refused, password_command broken)
// must not leave a file that says otherwise.
func applyEncryption(ctx context.Context, deps Deps, next config.Config, pass []byte) tea.Cmd {
	return func() tea.Msg {
		on := next.Encryption.Enabled
		done, err := deps.Encryption.Set(ctx, on, next.Encryption.KeySource, pass)
		if err != nil {
			verb := "turning encryption off"
			if on {
				verb = "turning encryption on"
			}
			return configSavedMsg{err: fmt.Errorf("%s: %w", verb, err)}
		}
		msg := saveConfig(ctx, deps, next)().(configSavedMsg)
		msg.encryption, msg.encryptionOn = done, on
		if !msg.saved {
			msg.err = fmt.Errorf("%w (encryption changed for now; it reverts at the next start)", msg.err)
		}
		return msg
	}
}

func waitEncryption(done <-chan error, seq int, on bool) tea.Cmd {
	return func() tea.Msg {
		return encryptionDoneMsg{seq: seq, on: on, err: <-done}
	}
}

// encryptionStarted notes a re-seal in the status line and waits for it.
func (m *Model) encryptionStarted(done <-chan error, on bool) tea.Cmd {
	m.encSeq++
	if on {
		m.notice, m.noticeIsErr = "Encrypting stored messages in the background…", false
	} else {
		m.notice, m.noticeIsErr = "Decrypting stored messages in the background…", false
	}
	return waitEncryption(done, m.encSeq, on)
}

// encryptionDone reports a finished re-seal. One replaced by a newer
// change is ignored; search re-checks whether it's available.
func (m Model) encryptionDone(msg encryptionDoneMsg) (Model, tea.Cmd) {
	if msg.seq != m.encSeq {
		return m, nil
	}
	var unreadable *store.ResealError
	switch {
	case errors.As(msg.err, &unreadable):
		// Retrying can't help: the same value fails every time.
		m.setError("changing encryption stopped: a stored message can't be read, so it stops there every time "+
			"(go-whassup db reset starts over; history older than your phone's sync is lost)", msg.err)
	case msg.err != nil:
		m.setError("changing encryption stopped (it's retried at the next start)", msg.err)
	case msg.on:
		m.notice, m.noticeIsErr = "Encryption is on: stored messages are encrypted.", false
	default:
		m.notice, m.noticeIsErr = "Encryption is off: messages decrypted, and search is back.", false
	}
	if m.screen != screenSearch {
		return m, nil
	}
	m.search.off, m.search.err, m.search.searched = false, "", false
	return m, probeSearch(m.ctx, m.deps.Store)
}

// passphrase is the Settings step that asks for a new passphrase (twice)
// before turning encryption on with key source "prompt".
type passphrase struct {
	next  config.Config
	input textinput.Model
	first []byte // the first entry, while asking again
	err   string
}

func newPassphrase(next config.Config) *passphrase {
	in := newInput()
	in.Prompt = "Passphrase: "
	in.EchoMode = textinput.EchoPassword
	in.SetVirtualCursor(true)
	in.CharLimit = 256
	in.SetWidth(40)
	return &passphrase{next: next, input: in}
}

// key handles a key on the passphrase step: done with the passphrase (ok)
// or cancelled (cancel). The caller zeroes nothing; Set does.
func (p *passphrase) key(msg tea.KeyPressMsg) (pass []byte, ok, cancel bool, cmd tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		clear(p.first)
		return nil, false, true, nil
	case msg.String() == "enter":
		v := []byte(p.input.Value())
		p.input.SetValue("")
		switch {
		case len(v) == 0:
			p.err = "Enter a passphrase (it can't be empty)."
		case p.first == nil:
			p.first, p.err = v, ""
			p.input.Prompt = "Again:      "
		case !bytes.Equal(p.first, v):
			clear(p.first)
			p.first, p.err = nil, "They didn't match; try again."
			p.input.Prompt = "Passphrase: "
		default:
			clear(p.first)
			return v, true, false, nil
		}
		return nil, false, false, nil
	}
	p.input, cmd = p.input.Update(msg)
	return nil, false, false, cmd
}

func (p *passphrase) view(width, height int) string {
	lines := []string{
		paneTitleStyle.Render("Passphrase for stored messages"), "",
		lipgloss.NewStyle().Width(min(width-4, 80)).Render(
			"With key source prompt, go-whassup asks for this passphrase every time it starts. " +
				"If you forget it, stored messages can't be read (go-whassup db reset starts over)."), "",
		p.input.View(),
	}
	if p.err != "" {
		lines = append(lines, "", errorStyle.Render(p.err))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

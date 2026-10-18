package ui

import (
	"context"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/config"
)

// settings is the Settings screen's state: a working copy of the config,
// edited field by field and saved on each committed change.
type settings struct {
	cfg      config.Config
	selected int
	editing  bool
	pending  *config.Config // a change awaiting y/n (see config.Field.ConfirmationFor)
	confirm  config.Confirmation
	// staged is a change that revealed an empty setting (key_source =
	// password-command shows password_command): it's held, and shown,
	// until that setting is filled in. Esc drops both. stagedFrom is the
	// row to return to.
	staged     *config.Config
	stagedFrom int
	input      textinput.Model
	pass       *passphrase // asking for a new passphrase (encryption, key source prompt)
	busy       string      // what's being applied, until configSavedMsg
	err        string
	saved      string // last confirmation
}

func newSettings(cfg config.Config) settings {
	in := textinput.New()
	in.Prompt = ""
	in.SetVirtualCursor(true)
	in.CharLimit = 512
	in.SetWidth(40)
	return settings{cfg: cfg, input: in}
}

type configSavedMsg struct {
	cfg   config.Config
	saved bool // the file was written (err may still report an apply failure)
	err   error
	// encryption, if set, is a re-seal the change started (see Encryption).
	encryption   <-chan error
	encryptionOn bool
}

// saveConfig writes cfg and applies it to the running app. Both happen off
// the UI goroutine; the result comes back as configSavedMsg.
func saveConfig(ctx context.Context, deps Deps, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if err := config.Save(deps.ConfigPath, cfg); err != nil {
			return configSavedMsg{err: err}
		}
		if deps.ApplyConfig != nil {
			if err := deps.ApplyConfig(ctx, cfg); err != nil {
				return configSavedMsg{cfg: cfg, saved: true, err: err}
			}
		}
		return configSavedMsg{cfg: cfg, saved: true}
	}
}

func (m Model) openSettings() Model {
	m.screen = screenSettings
	m.settings = newSettings(m.deps.Config)
	return m
}

func (s *settings) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case configSavedMsg:
		s.savedResult(r, msg)
		return false, nil
	case passphraseCheckedMsg:
		return false, s.passphraseChecked(r, msg)
	case tea.KeyPressMsg:
		if s.pass != nil {
			return false, s.passphraseKey(r, msg)
		}
		return s.key(r, msg)
	}
	var cmd tea.Cmd
	switch {
	case s.pass != nil:
		s.pass.input, cmd = s.pass.input.Update(msg)
	case s.editing:
		s.input, cmd = s.input.Update(msg)
	}
	return false, cmd
}

// passphraseChecked continues turning encryption on or off once it's known
// whether a passphrase must be typed first.
func (s *settings) passphraseChecked(r rootAPI, msg passphraseCheckedMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		s.busy, s.err = "", msg.err.Error()
		return nil
	case msg.needed:
		s.busy, s.pass = "", newPassphrase(msg.cfg)
		return s.pass.input.Focus()
	}
	return s.applyEncryption(r, msg.cfg, nil)
}

func (s *settings) passphraseKey(r rootAPI, msg tea.KeyPressMsg) tea.Cmd {
	pass, ok, cancel, cmd := s.pass.key(msg)
	switch {
	case cancel:
		s.pass = nil
	case ok:
		next := s.pass.next
		s.pass = nil
		return s.applyEncryption(r, next, pass)
	}
	return cmd
}

func (s *settings) applyEncryption(r rootAPI, next config.Config, pass []byte) tea.Cmd {
	s.busy = "Decrypting…"
	if next.Encryption.Enabled {
		s.busy = "Setting up encryption (your keychain may ask for permission)…"
	}
	return applyEncryption(r.ctx(), r.deps(), next, pass)
}

func (s *settings) help() []key.Binding {
	if s.editing || s.pass != nil {
		return formHelp
	}
	return settingsHelp
}

// shown is the config the page displays: a staged change, else the saved one.
func (s *settings) shown() *config.Config {
	if s.staged != nil {
		return s.staged
	}
	return &s.cfg
}

func (s *settings) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	fields := s.shown().Fields()
	if len(fields) == 0 {
		return true, nil
	}
	s.selected = min(s.selected, len(fields)-1)
	switch {
	case s.pending != nil:
		return false, s.confirmKey(r, msg)
	case s.editing:
		return false, s.editKey(r, msg)
	}
	switch {
	case key.Matches(msg, keys.Back):
		return true, nil
	case key.Matches(msg, keys.Up):
		s.selected = max(s.selected-1, 0)
		s.err = ""
	case key.Matches(msg, keys.Down):
		s.selected = min(s.selected+1, len(fields)-1)
		s.err = ""
	case key.Matches(msg, keys.Select), key.Matches(msg, settingsNext):
		return false, s.change(r, fields[s.selected], 1)
	case key.Matches(msg, settingsPrev):
		return false, s.change(r, fields[s.selected], -1)
	}
	return false, nil
}

// confirmKey answers the y/n question for a pending change.
func (s *settings) confirmKey(r rootAPI, msg tea.KeyPressMsg) tea.Cmd {
	next := *s.pending
	switch msg.String() {
	case "y":
		s.pending, s.confirm = nil, config.Confirmation{}
		return s.commit(r, next)
	case "n", "esc":
		s.pending, s.confirm = nil, config.Confirmation{}
	}
	return nil
}

// editKey handles typing into a field's text input.
func (s *settings) editKey(r rootAPI, msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Back):
		s.editing, s.err = false, ""
		s.input.Blur()
		if s.staged != nil { // drop the change that asked for this value
			s.staged, s.selected = nil, s.stagedFrom
		}
		return nil
	case msg.String() == "enter":
		next := *s.shown() // value copy; edit it, keep s.cfg until valid
		if err := next.Fields()[s.selected].Set(s.input.Value()); err != nil {
			s.err = err.Error()
			return nil
		}
		return s.commit(r, next)
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// change cycles a bool or choice (dir 1 forward, -1 back; asking first if
// the field says so), or starts editing a text field. A choice that
// reveals an empty setting is staged while that setting is asked for.
func (s *settings) change(r rootAPI, f config.Field, dir int) tea.Cmd {
	if f.Kind != config.KindBool && f.Kind != config.KindChoice {
		return s.edit(f)
	}
	next := s.cfg
	if dir < 0 {
		next.Fields()[s.selected].CycleBack()
	} else {
		next.Fields()[s.selected].Cycle()
	}
	if i, ok := revealedEmpty(&s.cfg, &next); ok {
		s.staged, s.stagedFrom, s.selected = &next, s.selected, i
		return s.edit(next.Fields()[i])
	}
	c, ask := f.ConfirmationFor(next.Fields()[s.selected].Value())
	if !ask {
		return s.commit(r, next)
	}
	if err := next.Validate(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.pending, s.confirm = &next, c
	return nil
}

// edit starts editing a text field.
func (s *settings) edit(f config.Field) tea.Cmd {
	s.editing, s.err = true, ""
	s.input.SetValue(f.Value())
	s.input.CursorEnd()
	return s.input.Focus()
}

// revealedEmpty finds a text setting that after lists but before doesn't
// (see config's shown_when), still empty: it must be filled in first.
func revealedEmpty(before, after *config.Config) (int, bool) {
	had := map[string]bool{}
	for _, f := range before.Fields() {
		had[f.Name()] = true
	}
	for i, f := range after.Fields() {
		if !had[f.Name()] && f.Kind == config.KindText && strings.TrimSpace(f.Value()) == "" {
			return i, true
		}
	}
	return 0, false
}

func (s *settings) commit(r rootAPI, next config.Config) tea.Cmd {
	if err := next.Validate(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.editing, s.err, s.saved = false, "", ""
	s.input.Blur()
	if enc := r.deps().Encryption; enc != nil && encryptionChanged(s.cfg, next) {
		s.busy = "…"
		return checkPassphrase(r.ctx(), enc, next)
	}
	return saveConfig(r.ctx(), r.deps(), next)
}

// savedResult shows the outcome of a save. (The root adopts the saved
// config even if this page has closed meanwhile.)
func (s *settings) savedResult(r rootAPI, msg configSavedMsg) {
	s.busy = ""
	staged := s.staged != nil
	s.staged = nil
	if !msg.saved {
		s.err = "saving: " + msg.err.Error()
		if staged {
			s.selected = s.stagedFrom
		}
		return
	}
	// Saved; adopt it even if applying partly failed, since the file now
	// holds it.
	s.cfg = msg.cfg
	s.saved = "Saved to " + tildePath(r.deps().ConfigPath)
	if msg.err != nil {
		s.err = "saved, but applying failed: " + msg.err.Error()
	}
}

func (s *settings) view(r rootAPI, width, height int) string {
	if s.pass != nil {
		return s.pass.view(width, height)
	}
	fields := s.shown().Fields()
	if s.pending != nil {
		// A confirmation gets the whole screen so it fits small terminals.
		c := s.confirm
		content := paneTitleStyle.Render(c.Question) + "\n\n" +
			lipgloss.NewStyle().Width(min(width-4, 80)).Render(c.Text) + "\n\n" +
			senderStyle.Render("y") + "  " + c.Yes + "\n" +
			senderStyle.Render("n") + "  " + c.No
		return pageStyle(width, height).Render(content)
	}
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("Settings") + "  " + faintStyle.Render(tildePath(r.deps().ConfigPath)) + "\n")

	keyW := 0
	for _, f := range fields {
		keyW = max(keyW, lipgloss.Width(f.Key))
	}
	section := ""
	for i, f := range fields {
		if f.Section != section {
			section = f.Section
			b.WriteString("\n" + senderStyle.Render("["+section+"]") + "\n")
		}
		value := f.Value()
		if f.Kind == config.KindChoice {
			value = "‹ " + value + " ›"
		}
		if i == s.selected && s.editing {
			value = s.input.View()
		}
		row := "  " + f.Key + strings.Repeat(" ", keyW-lipgloss.Width(f.Key)) + "  " + value
		if i == s.selected {
			row = activeSelStyle.Render("›" + row[1:])
		}
		b.WriteString(row + "\n")
	}

	if s.selected < len(fields) {
		f := fields[s.selected]
		b.WriteString("\n" + lipgloss.NewStyle().Width(min(width-4, 80)).Render(strings.ReplaceAll(f.Comment, "\n", " ")) + "\n")
		if f.PairingOnly {
			b.WriteString(faintStyle.Render("Applies the next time you link (L › Re-link).") + "\n")
		}
	}
	switch {
	case s.err != "":
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	case s.busy != "":
		b.WriteString("\n" + faintStyle.Render(s.busy) + "\n")
	case s.saved != "":
		b.WriteString("\n" + onlineStyle.Render(s.saved) + "\n")
	}
	return pageStyle(width, height).Render(b.String())
}

// tildePath shortens a path under the home directory to ~/....
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return p
}

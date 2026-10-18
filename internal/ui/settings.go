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
	pending  *config.Config // a change awaiting y/n because its field has a Confirm text
	confirm  string
	input    textinput.Model
	err      string
	saved    string // last confirmation
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
	case tea.KeyPressMsg:
		return s.key(r, msg)
	}
	if !s.editing {
		return false, nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return false, cmd
}

func (s *settings) help() []key.Binding {
	if s.editing {
		return formHelp
	}
	return settingsHelp
}

func (s *settings) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	fields := s.cfg.Fields()
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
	case key.Matches(msg, keys.Select):
		return false, s.change(r, fields[s.selected])
	}
	return false, nil
}

// confirmKey answers the y/n question for a pending change.
func (s *settings) confirmKey(r rootAPI, msg tea.KeyPressMsg) tea.Cmd {
	next := *s.pending
	switch msg.String() {
	case "y":
		s.pending, s.confirm = nil, ""
		return s.commit(r, next)
	case "n", "esc":
		s.pending, s.confirm = nil, ""
	}
	return nil
}

// editKey handles typing into a field's text input.
func (s *settings) editKey(r rootAPI, msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Back):
		s.editing, s.err = false, ""
		s.input.Blur()
		return nil
	case msg.String() == "enter":
		next := s.cfg // value copy; edit it, keep s.cfg until valid
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

// change cycles a bool or choice (asking first if the field says so), or
// starts editing a text field.
func (s *settings) change(r rootAPI, f config.Field) tea.Cmd {
	if f.Kind != config.KindBool && f.Kind != config.KindChoice {
		s.editing, s.err = true, ""
		s.input.SetValue(f.Value())
		s.input.CursorEnd()
		return s.input.Focus()
	}
	next := s.cfg
	next.Fields()[s.selected].Cycle()
	if f.Confirm == "" {
		return s.commit(r, next)
	}
	if err := next.Validate(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.pending, s.confirm = &next, f.Confirm
	return nil
}

func (s *settings) commit(r rootAPI, next config.Config) tea.Cmd {
	if err := next.Validate(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.editing, s.err, s.saved = false, "", ""
	s.input.Blur()
	return saveConfig(r.ctx(), r.deps(), next)
}

// savedResult shows the outcome of a save. (The root adopts the saved
// config even if this page has closed meanwhile.)
func (s *settings) savedResult(r rootAPI, msg configSavedMsg) {
	if !msg.saved {
		s.err = "saving: " + msg.err.Error()
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
	fields := s.cfg.Fields()
	if s.pending != nil {
		// A confirmation gets the whole screen so it fits small terminals.
		name := fields[s.selected].Name()
		content := paneTitleStyle.Render("Change "+name+"?") + "\n\n" +
			lipgloss.NewStyle().Width(min(width-4, 80)).Render(s.confirm) + "\n\n" +
			senderStyle.Render("y = yes, n = no")
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
	if s.err != "" {
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	} else if s.saved != "" {
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

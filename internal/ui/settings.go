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
	input    textinput.Model
	err      string
	saved    string // last confirmation
}

func newSettings(cfg config.Config) settings {
	in := textinput.New()
	in.Prompt = ""
	in.SetVirtualCursor(true)
	in.CharLimit = 9
	in.SetWidth(10)
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

func (m Model) settingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.settings
	fields := s.cfg.Fields()
	if len(fields) == 0 {
		m.screen = screenMain
		return m, nil
	}
	s.selected = min(s.selected, len(fields)-1)
	f := fields[s.selected]

	if s.editing {
		switch {
		case key.Matches(msg, keys.Back):
			s.editing, s.err = false, ""
			s.input.Blur()
			return m, nil
		case msg.String() == "enter":
			next := s.cfg // value copy; edit it, keep s.cfg until valid
			nf := next.Fields()[s.selected]
			if err := nf.Set(s.input.Value()); err != nil {
				s.err = err.Error()
				return m, nil
			}
			return m.commitSetting(next)
		}
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return m, cmd
	}

	switch {
	case key.Matches(msg, keys.Back):
		m.screen = screenMain
	case key.Matches(msg, keys.Up):
		s.selected = max(s.selected-1, 0)
		s.err = ""
	case key.Matches(msg, keys.Down):
		s.selected = min(s.selected+1, len(fields)-1)
		s.err = ""
	case key.Matches(msg, keys.Select):
		if f.Kind == config.KindBool || f.Kind == config.KindChoice {
			next := s.cfg
			next.Fields()[s.selected].Cycle()
			return m.commitSetting(next)
		}
		s.editing, s.err = true, ""
		s.input.SetValue(f.Value())
		s.input.CursorEnd()
		return m, s.input.Focus()
	}
	return m, nil
}

// commitSetting validates next and saves it, or shows why it's invalid and
// keeps the previous value.
func (m Model) commitSetting(next config.Config) (tea.Model, tea.Cmd) {
	if err := next.Validate(); err != nil {
		m.settings.err = err.Error()
		return m, nil
	}
	m.settings.editing, m.settings.err, m.settings.saved = false, "", ""
	m.settings.input.Blur()
	return m, saveConfig(m.ctx, m.deps, next)
}

func (m Model) settingsSaved(msg configSavedMsg) (tea.Model, tea.Cmd) {
	if !msg.saved {
		m.settings.err = "saving: " + msg.err.Error()
		return m, nil
	}
	// Saved; adopt it even if applying partly failed, since the file now
	// holds it.
	m.settings.cfg, m.deps.Config = msg.cfg, msg.cfg
	m.settings.saved = "Saved to " + tildePath(m.deps.ConfigPath)
	if msg.err != nil {
		m.settings.err = "saved, but applying failed: " + msg.err.Error()
	}
	// Retention may have pruned messages.
	return m, loadChats(m.ctx, m.deps.Store)
}

func (m Model) settingsView(width, height int) string {
	s := m.settings
	fields := s.cfg.Fields()
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("Settings") + "  " + faintStyle.Render(tildePath(m.deps.ConfigPath)) + "\n")

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
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1).Render(b.String())
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

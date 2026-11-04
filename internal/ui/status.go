package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// helpView shows complete descriptions, with scrolling on small terminals.
func (m Model) helpView(height int) string {
	lines := strings.Split(m.helpBody(), "\n")
	rows := max(height-5, 1)
	start := min(m.helpScroll, max(len(lines)-rows, 0))
	body := strings.Join(lines[start:min(start+rows, len(lines))], "\n")
	note := faintStyle.Render("↑/↓ scroll · F1/Esc close")
	return pageStyle(m.width, height).Render(paneTitleStyle.Render("Keys") + "\n\n" + body + "\n\n" + note)
}

func renderHelpSection(s helpSection, width int) string {
	keyW := 0
	for _, b := range s.keys {
		keyW = max(keyW, lipgloss.Width(b.Help().Key))
	}
	lines := []string{senderStyle.Render(s.title)}
	for _, b := range s.keys {
		k := b.Help().Key
		description := strings.Split(lipgloss.NewStyle().Width(max(width-keyW-4, 1)).Render(b.Help().Desc), "\n")
		lines = append(lines, "  "+fromMeStyle.Render(k)+strings.Repeat(" ", keyW-lipgloss.Width(k))+"  "+description[0])
		for _, line := range description[1:] {
			lines = append(lines, strings.Repeat(" ", keyW+4)+line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) helpBody() string {
	sections := mainHelpSections
	if m.screen != screenMain {
		sections = []helpSection{{"Current screen", m.contextBindings()}, mainHelpSections[len(mainHelpSections)-1]}
	}
	width := max(m.width-4, 12)
	if width < 96 {
		var parts []string
		for _, section := range sections {
			parts = append(parts, renderHelpSection(section, width))
		}
		return strings.Join(parts, "\n\n")
	}
	colWidth := (width - 4) / 2
	var columns [2]string
	for _, section := range sections {
		column := 0
		if lipgloss.Height(columns[0]) > lipgloss.Height(columns[1]) {
			column = 1
		}
		if columns[column] != "" {
			columns[column] += "\n\n"
		}
		columns[column] += renderHelpSection(section, colWidth)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, columns[0], "    ", columns[1])
}

func (m Model) helpKey(k tea.KeyPressMsg) Model {
	maxScroll := max(lipgloss.Height(m.helpBody())-max(m.bodyHeight()-5, 1), 0)
	switch {
	case k.Code == tea.KeyUp:
		m.helpScroll = max(m.helpScroll-1, 0)
	case k.Code == tea.KeyDown:
		m.helpScroll = min(m.helpScroll+1, maxScroll)
	case key.Matches(k, keys.PageUp):
		m.helpScroll = max(m.helpScroll-max(m.bodyHeight()-5, 1), 0)
	case key.Matches(k, keys.PageDown):
		m.helpScroll = min(m.helpScroll+max(m.bodyHeight()-5, 1), maxScroll)
	default:
		m.showHelp = false
	}
	return m
}

// notice is the status line's message: an error, a hint, or an outcome.
// A scoped one is about what's selected right now ("File opened.",
// "Copied …"), so the next move clears it (clearScoped); the rest stay
// until replaced.
type notice struct {
	text   string
	err    bool
	scoped bool
}

// say shows text in the status line until it's replaced.
func (m *Model) say(text string) { m.notice = notice{text: text} }

// sayScoped shows text about the current selection; see clearScoped.
func (m *Model) sayScoped(text string) { m.notice = notice{text: text, scoped: true} }

// clearScoped drops a notice about the previous selection, keeping
// errors and hints.
func (m *Model) clearScoped() {
	if m.notice.scoped {
		m.notice = notice{}
	}
}

func (m Model) statusLine() string {
	var state string
	switch m.conn {
	case connOnline:
		state = onlineStyle.Render("● online")
	case connConnecting:
		state = statusStyle.Render("○ connecting…")
	case connOffline:
		state = errorStyle.Render("○ offline")
	case connNotPaired:
		state = errorStyle.Render("○ not linked")
		if m.screen != screenLogin {
			state = errorStyle.Render("○ not linked: press L")
		}
	case connLoggedOut:
		state = errorStyle.Render("○ logged out")
	}
	parts := []string{state}
	if n := m.sub.Dropped(); n > 0 {
		parts = append(parts, errorStyle.Render(fmt.Sprintf("%d events dropped", n)))
	}
	if m.notice.text != "" {
		style := statusStyle
		if m.notice.err {
			style = errorStyle
		}
		parts = append(parts, style.Render(m.notice.text))
	}
	// Hints fill what's left, dropping whole ones ("…") rather than
	// cutting one mid-word.
	h := m.help
	h.SetWidth(max(m.width-lipgloss.Width(strings.Join(parts, "  "))-2, 1))
	parts = append(parts, h.ShortHelpView(m.contextHelp()))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(parts, "  "))
}

// contextHelp returns the bindings that apply right now.
func (m Model) contextHelp() []key.Binding {
	if m.showHelp {
		return nil
	}
	bindings := m.contextBindings()
	if !slices.ContainsFunc(bindings, func(b key.Binding) bool { return b.Help().Key == keys.Help.Help().Key }) {
		bindings = append(slices.Clone(bindings), keys.Help)
	}
	return bindings
}

func (m Model) contextBindings() []key.Binding {
	switch {
	case m.screen == screenLogin:
		switch m.login.step {
		case loginPhone, loginFailed:
			return formHelp
		case loginRequesting, loginCode, loginQR:
			return waitHelp
		case loginUnlinking, loginFinishing:
			return nil
		case loginConfirm:
			return []key.Binding{keys.Back}
		}
		return menuHelp
	case m.page() != nil:
		return m.page().help()
	case m.focus == focusInput && m.conv.reacting:
		return reactHelp
	case m.focus == focusInput && m.conv.confirmingDelete:
		return deleteHelp
	case m.focus == focusInput && m.conv.picking:
		return pickHelp
	case m.focus == focusInput:
		return m.mainHelp(inputHelp)
	default:
		return m.mainHelp(listHelp)
	}
}

// mainHelp is the main screen's hints, without the chat shortcuts that
// can't do anything right now: attachments, info and selecting a message need a
// selected chat, and a draft has no messages yet (but can be sent a file).
func (m Model) mainHelp(bindings []key.Binding) []key.Binding {
	var hide []key.Binding
	switch {
	case m.conv.chat.JID == "":
		hide = []key.Binding{keys.Media, keys.Attach, keys.Info, keys.Pick, keys.Voice}
	case m.draft != "":
		hide = []key.Binding{keys.Media, keys.Info, keys.Pick} // nothing to reply or react to yet
	}
	return slices.DeleteFunc(slices.Clone(bindings), func(b key.Binding) bool {
		return slices.ContainsFunc(hide, func(h key.Binding) bool { return h.Help().Key == b.Help().Key })
	})
}

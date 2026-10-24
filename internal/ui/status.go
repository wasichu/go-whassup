package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// helpView is the ? overlay: every main-screen key, by section, side by
// side when there's room.
func (m Model) helpView(height int) string {
	list := renderHelpSection(mainHelpSections[0])
	rest := renderHelpSection(mainHelpSections[1]) + "\n\n" + renderHelpSection(mainHelpSections[2])
	body := list + "\n\n" + rest
	if lipgloss.Width(list)+lipgloss.Width(rest)+6 <= m.width-2 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, "      ", rest)
	}
	note := faintStyle.Render("Letter keys work on the chat list; in the message box they're typed. Press any key to close.")
	return pageStyle(m.width, height).Render(paneTitleStyle.Render("Keys") + "\n\n" + body + "\n\n" + note)
}

// renderHelpSection lists a section's keys under its title, aligned.
func renderHelpSection(s helpSection) string {
	keyW := 0
	for _, b := range s.keys {
		keyW = max(keyW, lipgloss.Width(b.Help().Key))
	}
	lines := []string{senderStyle.Render(s.title)}
	for _, b := range s.keys {
		k := b.Help().Key
		lines = append(lines, "  "+fromMeStyle.Render(k)+strings.Repeat(" ", keyW-lipgloss.Width(k))+"  "+b.Help().Desc)
	}
	return strings.Join(lines, "\n")
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
	switch {
	case m.showHelp:
		return nil
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
		hide = []key.Binding{keys.Media, keys.Attach, keys.Info, keys.Pick}
	case m.draft != "":
		hide = []key.Binding{keys.Media, keys.Info, keys.Pick} // nothing to reply or react to yet
	}
	return slices.DeleteFunc(slices.Clone(bindings), func(b key.Binding) bool {
		return slices.ContainsFunc(hide, func(h key.Binding) bool { return h.Help().Key == b.Help().Key })
	})
}

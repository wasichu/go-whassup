package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

func (m Model) helpView(height int) string {
	content := paneTitleStyle.Render("Keys") + "\n\n" + m.help.FullHelpView(fullMainHelp) +
		"\n\n" + faintStyle.Render("q / s o a i , ? and L only act from the chat list; in the message box they're typed. Press any key to close.")
	return pageStyle(m.width, height).Render(content)
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
	if m.notice != "" {
		style := statusStyle
		if m.noticeIsErr {
			style = errorStyle
		}
		parts = append(parts, style.Render(m.notice))
	}
	parts = append(parts, m.help.ShortHelpView(m.contextHelp()))
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
	case m.focus == focusInput:
		return inputHelp
	default:
		return listHelp
	}
}

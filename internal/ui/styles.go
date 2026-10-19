package ui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// Colors use ANSI 16-color indexes so they follow the user's terminal theme.
var (
	accent = lipgloss.Color("4") // blue
	muted  = lipgloss.Color("8") // bright black / grey

	paneTitleStyle   = lipgloss.NewStyle().Bold(true)
	faintStyle       = lipgloss.NewStyle().Foreground(muted)
	selectedStyle    = lipgloss.NewStyle().Reverse(true)
	activeSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(accent)
	fromMeStyle      = lipgloss.NewStyle().Foreground(accent).Bold(true)
	senderStyle      = lipgloss.NewStyle().Bold(true)
	daySepStyle      = lipgloss.NewStyle().Foreground(muted).Align(lipgloss.Center)
	separatorStyle   = lipgloss.NewStyle().Foreground(muted)
	statusStyle      = lipgloss.NewStyle().Foreground(muted)
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	onlineStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	unreadBadgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("2")).Bold(true)
	mutedBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(muted)
	newDividerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Align(lipgloss.Center)
	deletedStyle     = lipgloss.NewStyle().Foreground(muted).Italic(true)
	reactionStyle    = lipgloss.NewStyle().Foreground(muted)
	myReactionStyle  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mentionStyle     = lipgloss.NewStyle().Foreground(accent)
	myMentionStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(accent).Bold(true)
	highlightStyle   = lipgloss.NewStyle().Reverse(true)
	matchStyle       = lipgloss.NewStyle().Bold(true).Underline(true)
	codeStyle        = lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 2)
)

// pageStyle fills a full-screen page, clipping to its height.
func pageStyle(width, height int) lipgloss.Style {
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1)
}

// newInput is a text input with a solid (non-blinking) cursor, used for
// every input so they all behave alike.
func newInput() textinput.Model {
	in := textinput.New()
	s := in.Styles()
	s.Cursor.Blink = false
	in.SetStyles(s)
	return in
}

package ui

import "charm.land/lipgloss/v2"

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
	newDividerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Align(lipgloss.Center)
	codeStyle        = lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 2)
)

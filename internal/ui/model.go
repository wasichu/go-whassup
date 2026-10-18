// Package ui holds the Bubble Tea models. It must never import whatsmeow;
// it talks to the protocol layer only through internal/wa interfaces and
// receives asynchronous events as tea.Msg values.
package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	hintStyle  = lipgloss.NewStyle().Faint(true)
)

// Model is the root TUI model.
type Model struct {
	width, height int
}

// New returns the root model.
func New() Model {
	return Model{}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("go-whassup"),
		"Not connected.",
		hintStyle.Render("q: quit"),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "go-whassup"
	return v
}

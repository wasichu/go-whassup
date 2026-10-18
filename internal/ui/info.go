package ui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
)

// info is the "i" screen: who a chat is with.
type info struct {
	details store.ChatDetails
	loaded  bool
	offset  int // first member shown
}

type detailsLoadedMsg struct {
	details store.ChatDetails
	err     error
}

func loadDetails(ctx context.Context, st Store, chat string) tea.Cmd {
	return func() tea.Msg {
		d, err := st.Details(ctx, chat)
		return detailsLoadedMsg{details: d, err: err}
	}
}

func (m Model) openInfo() (Model, tea.Cmd) {
	if m.conv.chat.JID == "" || m.draft != "" {
		return m, nil
	}
	m.screen, m.info = screenInfo, info{}
	return m, loadDetails(m.ctx, m.deps.Store, m.conv.chat.JID)
}

func (m Model) infoKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back), msg.String() == "i", msg.String() == "q":
		m.screen = screenMain
	case key.Matches(msg, keys.Up):
		m.info.offset = max(m.info.offset-1, 0)
	case key.Matches(msg, keys.Down):
		m.info.offset = min(m.info.offset+1, max(len(m.info.details.Members)-1, 0))
	}
	return m, nil
}

func (m Model) infoView(width, height int) string {
	if !m.info.loaded {
		return lipgloss.NewStyle().Width(width).Height(height).Padding(0, 1).Render("Loading…")
	}
	d := m.info.details
	user, _, _ := strings.Cut(d.JID, "@")
	lines := []string{paneTitleStyle.Render(chatTitle(d.ChatSummary, m.ownJID)), ""}
	if !d.IsGroup {
		lines = append(lines, "Number:  +"+user)
		if d.PushName != "" && d.PushName != d.Name {
			lines = append(lines, "Calls themselves: "+d.PushName)
		}
	} else {
		if d.Topic != "" {
			lines = append(lines, lipgloss.NewStyle().Width(min(width-4, 80)).Render(d.Topic), "")
		}
		lines = append(lines, senderStyle.Render(fmt.Sprintf("%d members", len(d.Members))))
		if len(d.Members) == 0 {
			lines = append(lines, faintStyle.Render("Member list arrives after go-whassup connects."))
		}
		rows := max(height-len(lines)-1, 1)
		for i := m.info.offset; i < len(d.Members) && i < m.info.offset+rows; i++ {
			mem := d.Members[i]
			muser, _, _ := strings.Cut(mem.JID, "@")
			name := mem.Name
			if name == "" {
				name = "+" + muser
			}
			if mem.JID == m.ownJID {
				name += " (You)"
			}
			line := "  " + name
			if mem.Name != "" {
				line += "  " + faintStyle.Render("+"+muser)
			}
			if mem.Admin {
				line += "  " + onlineStyle.Render("admin")
			}
			lines = append(lines, line)
		}
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

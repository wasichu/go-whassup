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

func (in *info) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case detailsLoadedMsg:
		if msg.err != nil {
			r.setError("loading chat info", msg.err)
			return true, nil
		}
		in.details, in.loaded = msg.details, true
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Back), msg.String() == "i", msg.String() == "q":
			return true, nil
		case key.Matches(msg, keys.Up):
			in.offset = max(in.offset-1, 0)
		case key.Matches(msg, keys.Down):
			in.offset = min(in.offset+1, max(len(in.details.Members)-1, 0))
		}
	}
	return false, nil
}

func (*info) help() []key.Binding { return []key.Binding{keys.Up, keys.Down, keys.Back} }

func (in *info) view(r rootAPI, width, height int) string {
	if !in.loaded {
		return lipgloss.NewStyle().Width(width).Height(height).Padding(0, 1).Render("Loading…")
	}
	d := in.details
	lines := []string{paneTitleStyle.Render(chatTitle(d.ChatSummary, r.own())), ""}
	if !d.IsGroup {
		lines = append(lines, "Number:  "+store.NumberOf(d.JID))
		if d.PushName != "" && "~"+d.PushName != d.Name && d.PushName != d.Name {
			lines = append(lines, "Calls themselves: "+d.PushName)
		}
		return pageStyle(width, height).Render(strings.Join(lines, "\n"))
	}
	if d.Topic != "" {
		lines = append(lines, lipgloss.NewStyle().Width(min(width-4, 80)).Render(d.Topic), "")
	}
	lines = append(lines, senderStyle.Render(fmt.Sprintf("%d members", len(d.Members))))
	if len(d.Members) == 0 {
		lines = append(lines, faintStyle.Render("Member list arrives after go-whassup connects."))
	}
	rows := max(height-len(lines)-1, 1)
	for i := in.offset; i < len(d.Members) && i < in.offset+rows; i++ {
		lines = append(lines, memberLine(d.Members[i], r.own()))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

// memberLine is one group member: name (or number, dimmed), number, admin.
// A privacy ID has no number to show.
func memberLine(mem store.Member, own string) string {
	number := store.NumberOf(mem.JID)
	name, named := store.PersonLabel(mem.Name, "", mem.JID)
	if mem.JID == own {
		name = strings.TrimPrefix(name, "~") + " (You)"
	}
	line := "  " + nameStyle(named, lipgloss.NewStyle()).Render(name)
	if mem.Name != "" && !store.IsPrivacyID(mem.JID) {
		line += "  " + faintStyle.Render(number)
	}
	if mem.Admin {
		line += "  " + onlineStyle.Render("admin")
	}
	return line
}

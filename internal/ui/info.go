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
			w, h := r.pageSize()
			_, members, rows := in.layout(r, w, h)
			in.offset = min(in.offset+1, maxScroll(len(members), rows))
		}
	}
	return false, nil
}

func (*info) help() []key.Binding { return []key.Binding{keys.Up, keys.Down, keys.Back} }

func (in *info) view(r rootAPI, width, height int) string {
	if !in.loaded {
		return lipgloss.NewStyle().Width(width).Height(height).Padding(0, 1).Render("Loading…")
	}
	head, members, rows := in.layout(r, width, height)
	return pageStyle(width, height).Render(strings.Join(append(head, scrollWindow(members, in.offset, rows)...), "\n"))
}

// layout is the page's fixed top (title, topic, member count), the member
// lines, and how many rows of the page those get. update uses it too, to
// stop scrolling at the end.
func (in *info) layout(r rootAPI, width, height int) (head, members []string, rows int) {
	d := in.details
	head = []string{paneTitleStyle.Render(chatTitle(d.ChatSummary, r.own())), ""}
	if !d.IsGroup {
		head = append(head, "Number:  "+store.NumberOf(d.JID))
		if d.PushName != "" && "~"+d.PushName != d.Name && d.PushName != d.Name {
			head = append(head, "Calls themselves: "+d.PushName)
		}
		return head, nil, 0
	}
	if d.Topic != "" {
		head = append(head, lipgloss.NewStyle().Width(min(width-4, 80)).Render(d.Topic), "")
	}
	head = append(head, senderStyle.Render(fmt.Sprintf("%d members", len(d.Members))))
	if len(d.Members) == 0 {
		head = append(head, faintStyle.Render("Member list arrives after go-whassup connects."))
	}
	for _, mem := range d.Members {
		members = append(members, memberLine(mem, r.own()))
	}
	// A long topic wraps, so the head is measured, not counted.
	return head, members, max(height-lipgloss.Height(strings.Join(head, "\n"))-1, 2)
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

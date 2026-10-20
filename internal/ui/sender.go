package ui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// senderCard is "i" while picking a message: who sent it, with the names
// we know and their number (which c copies), and who reacted to it.
type senderCard struct {
	msg      wa.Message
	jid      string
	person   store.Person
	reactors []store.Reactor
	loaded   bool
	copied   bool
}

type senderLoadedMsg struct {
	person   store.Person
	reactors []store.Reactor
	err      error
}

var senderHelp = []key.Binding{
	key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy number")),
	keys.Back,
}

func loadSender(ctx context.Context, st Store, jid string, m wa.Message) tea.Cmd {
	return func() tea.Msg {
		p, err := st.Person(ctx, jid)
		if err != nil {
			return senderLoadedMsg{err: err}
		}
		rs, err := st.Reactors(ctx, m.Chat, m.ID)
		return senderLoadedMsg{person: p, reactors: rs, err: err}
	}
}

// openSender shows the card for the picked message's sender (you, for your
// own messages). Picking resumes when the card closes.
func (m Model) openSender(target wa.Message) (Model, tea.Cmd) {
	jid := target.Sender
	if target.FromMe && m.ownJID != "" {
		jid = m.ownJID
	}
	m.screen, m.sender = screenSender, senderCard{msg: target, jid: jid}
	return m, loadSender(m.ctx, m.deps.Store, jid, target)
}

func (s *senderCard) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case senderLoadedMsg:
		if msg.err != nil {
			r.setError("looking up the sender", msg.err)
			return true, nil
		}
		s.person, s.reactors, s.loaded = msg.person, msg.reactors, true
		if s.person.PushName == "" {
			s.person.PushName = s.msg.PushName // sent with the message, not saved as a contact
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Back), msg.String() == "i", msg.String() == "q":
			return true, nil
		case msg.String() == "c" && !store.IsPrivacyID(s.jid):
			s.copied = true
			return false, tea.SetClipboard(store.NumberOf(s.jid))
		}
	}
	return false, nil
}

// help offers copying only when there's a number to copy.
func (s *senderCard) help() []key.Binding {
	if store.IsPrivacyID(s.jid) {
		return []key.Binding{keys.Back}
	}
	return senderHelp
}

func (s *senderCard) view(r rootAPI, width, height int) string {
	who := senderName(s.msg, r.chat())
	if s.msg.FromMe {
		who = "You"
	}
	lines := []string{paneTitleStyle.Render(who), ""}
	if !s.loaded {
		lines = append(lines, faintStyle.Render("Loading…"))
		return pageStyle(width, height).Render(strings.Join(lines, "\n"))
	}
	p := s.person
	if p.Name != "" {
		lines = append(lines, "In your contacts as: "+p.Name)
	} else {
		lines = append(lines, faintStyle.Render("Not in your contacts"))
	}
	if p.PushName != "" {
		lines = append(lines, "Calls themselves:    ~"+p.PushName)
	}
	if p.BusinessName != "" {
		lines = append(lines, "Business:            "+p.BusinessName)
	}
	if store.IsPrivacyID(s.jid) {
		lines = append(lines, "Number:              "+faintStyle.Render("hidden by WhatsApp (this group uses privacy IDs)"))
	} else {
		lines = append(lines, "Number:              "+store.NumberOf(s.jid))
	}
	lines = append(lines, reactorLines(s.reactors)...)
	if s.copied {
		lines = append(lines, "", faintStyle.Render("Copied to the clipboard (if your terminal allows it)."))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n") + "\n\n" + keyLine(s.help()))
}

// reactorLines lists who reacted, oldest first: "  👍  Ada", with your own
// as "You" and unnamed people dimmed, as elsewhere.
func reactorLines(rs []store.Reactor) []string {
	if len(rs) == 0 {
		return nil
	}
	lines := []string{"", "Reactions:"}
	for _, r := range rs {
		name, named := store.PersonLabel(r.Name, "", r.JID)
		if r.Mine {
			name, named = "You", true
		}
		lines = append(lines, "  "+r.Emoji+"  "+nameStyle(named, lipgloss.NewStyle()).Render(name))
	}
	return lines
}

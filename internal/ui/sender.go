package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	offset   int // reaction lines scrolled past
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
		case key.Matches(msg, keys.Up):
			s.offset = max(s.offset-1, 0)
		case key.Matches(msg, keys.Down):
			w, h := r.pageSize()
			_, reactions, rows := s.layout(r, w, h)
			s.offset = min(s.offset+1, maxScroll(len(reactions), rows))
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
	head, reactions, rows := s.layout(r, width, height)
	if !s.loaded {
		return pageStyle(width, height).Render(strings.Join(head, "\n"))
	}
	lines := append(head, scrollWindow(reactions, s.offset, rows)...)
	return pageStyle(width, height).Render(strings.Join(lines, "\n") + "\n\n" + keyLine(s.help()))
}

// layout is the card's fixed top (who sent it), its reaction lines, and
// how many rows of the page those get. update uses it too, to stop
// scrolling at the end.
func (s *senderCard) layout(r rootAPI, width, height int) (head, reactions []string, rows int) {
	head = s.about(r)
	if !s.loaded || len(s.reactors) == 0 {
		return head, nil, 0
	}
	head = append(head, "", fmt.Sprintf("Reactions (%d):", len(s.reactors)))
	reactions = reactionLines(groupReactors(s.reactors), width-2) // the page's padding
	return head, reactions, max(height-len(head)-2, 2)            // a blank line and the keys below
}

// about is who sent the message: the names we know and their number.
func (s *senderCard) about(r rootAPI) []string {
	who := senderName(s.msg, r.chat())
	if s.msg.FromMe {
		who = "You"
	}
	lines := []string{paneTitleStyle.Render(who), ""}
	if !s.loaded {
		return append(lines, faintStyle.Render("Loading…"))
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
	if s.copied {
		lines = append(lines, faintStyle.Render("Copied to the clipboard (if your terminal allows it)."))
	}
	return lines
}

// reactionGroup is everyone who reacted with one emoji.
type reactionGroup struct {
	emoji    string
	reactors []store.Reactor
}

// groupReactors groups reactions by emoji as WhatsApp does: the most
// popular first (ties in order of first use), you first within yours.
func groupReactors(rs []store.Reactor) []reactionGroup {
	var gs []reactionGroup
	for _, r := range rs {
		i := slices.IndexFunc(gs, func(g reactionGroup) bool { return g.emoji == r.Emoji })
		if i < 0 {
			gs = append(gs, reactionGroup{emoji: r.Emoji})
			i = len(gs) - 1
		}
		if r.Mine {
			gs[i].reactors = slices.Insert(gs[i].reactors, 0, r)
			continue
		}
		gs[i].reactors = append(gs[i].reactors, r)
	}
	slices.SortStableFunc(gs, func(a, b reactionGroup) int { return len(b.reactors) - len(a.reactors) })
	return gs
}

// reactorName is how a reactor is shown: "You", or their name, dimmed
// when it's only a number.
func reactorName(r store.Reactor) string {
	if r.Mine {
		return "You"
	}
	name, named := store.PersonLabel(r.Name, "", r.JID)
	return nameStyle(named, lipgloss.NewStyle()).Render(name)
}

// reactionLines renders groups as "  👍 12  Ada, ~Bobby, …", wrapping
// whole names onto lines indented under the first, within width.
func reactionLines(gs []reactionGroup, width int) []string {
	digits := 1
	for _, g := range gs {
		digits = max(digits, len(strconv.Itoa(len(g.reactors))))
	}
	var lines []string
	for _, g := range gs {
		prefix := fmt.Sprintf("  %s %*d  ", g.emoji, digits, len(g.reactors))
		indent := strings.Repeat(" ", ansi.StringWidth(prefix))
		line := prefix
		for i, r := range g.reactors {
			name := reactorName(r)
			if i < len(g.reactors)-1 {
				name += ","
			}
			switch {
			case i == 0:
				line += name
			case ansi.StringWidth(line)+1+ansi.StringWidth(name) <= width:
				line += " " + name
			default:
				lines = append(lines, ansi.Truncate(line, width, "…"))
				line = indent + name
			}
		}
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	return lines
}

// maxScroll is how far lines can scroll in rows: until the last line
// shows above the "more" line.
func maxScroll(n, rows int) int {
	if n <= rows {
		return 0
	}
	return n - (rows - 1)
}

// scrollWindow is the rows of lines shown from offset; when they don't all
// fit, the last row says how many more are below (or above, at the end).
func scrollWindow(lines []string, offset, rows int) []string {
	if len(lines) <= rows {
		return lines
	}
	start := min(offset, maxScroll(len(lines), rows))
	visible := rows - 1
	out := slices.Clone(lines[start : start+visible])
	if below := len(lines) - start - visible; below > 0 {
		return append(out, faintStyle.Render(fmt.Sprintf("  ↓ %d more (↑/↓ scroll)", below)))
	}
	return append(out, faintStyle.Render(fmt.Sprintf("  ↑ %d more (↑/↓ scroll)", start)))
}

package ui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/store"
)

// finder is the "/" screen: fuzzy-find any chat or contact and open it.
type finder struct {
	input    textinput.Model
	all      []store.ChatSummary
	matches  []store.ChatSummary
	selected int
	loaded   bool
	own      string // the linked account, labelled "(You)"
}

func newFinder() finder {
	in := textinput.New()
	in.Prompt = "Find: "
	in.Placeholder = "name or number"
	in.SetVirtualCursor(true)
	in.CharLimit = 64
	return finder{input: in}
}

type directoryLoadedMsg struct {
	entries []store.ChatSummary
	err     error
}

func loadDirectory(ctx context.Context, st Store) tea.Cmd {
	return func() tea.Msg {
		entries, err := st.Directory(ctx)
		return directoryLoadedMsg{entries: entries, err: err}
	}
}

func (m Model) openFinder() (Model, tea.Cmd) {
	m.screen = screenFinder
	m.finder = newFinder()
	m.finder.own = m.ownJID
	m.finder.input.SetWidth(max(m.width-12, 10))
	cmd := tea.Batch(m.finder.input.Focus(), loadDirectory(m.ctx, m.deps.Store)) // evaluate before m is copied into the return
	return m, cmd
}

func (f *finder) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case directoryLoadedMsg:
		if msg.err != nil {
			r.setError("loading contacts", msg.err)
			return false, nil
		}
		f.all, f.loaded = msg.entries, true
		f.refilter()
		return false, nil
	case tea.KeyPressMsg:
		return f.key(r, msg)
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return false, cmd
}

func (*finder) help() []key.Binding { return finderHelp }

func (f *finder) refilter() {
	f.matches = filterDirectory(f.all, f.input.Value(), f.own)
	f.selected = min(f.selected, max(len(f.matches)-1, 0))
}

func (f *finder) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		return true, nil
	case msg.String() == "up", msg.String() == "ctrl+p":
		f.selected = max(f.selected-1, 0)
		return false, nil
	case msg.String() == "down", msg.String() == "ctrl+n":
		f.selected = min(f.selected+1, max(len(f.matches)-1, 0))
		return false, nil
	case msg.String() == "enter":
		if f.selected >= len(f.matches) {
			return false, nil
		}
		return true, r.openChat(f.matches[f.selected])
	}
	// Everything else edits the query (j/k are letters here, not navigation).
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	f.refilter()
	return false, cmd
}

// openChat shows c and focuses the message input. A contact without a chat
// opens as a draft; it becomes a regular chat once a message is sent.
func (m Model) openChat(c store.ChatSummary) (Model, tea.Cmd) {
	m.screen = screenMain
	if m.chats.selectJID(c.JID) {
		m.setDraft(nil)
		sel, _ := m.chats.selectedChat()
		m.conv.setChat(sel)
		m.focus = focusInput
		cmd := tea.Batch(m.conv.input.Focus(), loadMessages(m.ctx, m.deps, sel.JID), m.scheduleRead()) // evaluate before m is copied into the return
		return m, cmd
	}
	m.setDraft(&c)
	m.conv.setChat(c)
	m.conv.setMessages(nil)
	m.focus = focusInput
	cmd := m.conv.input.Focus() // evaluate before m is copied into the return
	return m, cmd
}

// setDraft opens c as a draft (nil ends it): the open chat has no messages
// yet, and the chat list shows it as a placeholder row.
func (m *Model) setDraft(c *store.ChatSummary) {
	m.draft, m.chats.draft = "", nil
	if c != nil {
		m.draft, m.chats.draft = c.JID, c
	}
}

func (f *finder) view(_ rootAPI, width, height int) string {
	lines := []string{paneTitleStyle.Render("Find a chat or contact"), "", f.input.View(), ""}
	switch {
	case !f.loaded:
		lines = append(lines, faintStyle.Render("Loading…"))
	case len(f.matches) == 0 && len(f.all) == 0:
		lines = append(lines, faintStyle.Render("No chats or contacts yet. They arrive after linking."))
	case len(f.matches) == 0:
		lines = append(lines, faintStyle.Render("No matches."))
	}
	rows := max(height-len(lines)-1, 1)
	start := 0
	if f.selected >= rows {
		start = f.selected - rows + 1
	}
	for i := start; i < len(f.matches) && i < start+rows; i++ {
		c := f.matches[i]
		user, _, _ := strings.Cut(c.JID, "@")
		tag := ""
		switch {
		case c.IsGroup:
			tag = "group"
		case c.LastMessageAt.Unix() <= 0:
			tag = "new chat · +" + user
		default:
			tag = "+" + user
		}
		title := chatTitle(c, f.own)
		label := fmt.Sprintf("%s  %s", title, faintStyle.Render(tag))
		if i == f.selected {
			label = activeSelStyle.Render("› "+title) + "  " + faintStyle.Render(tag)
		} else {
			label = "  " + label
		}
		lines = append(lines, label)
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

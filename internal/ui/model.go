// Package ui holds the Bubble Tea models. It must never import whatsmeow;
// it talks to the protocol layer only through internal/wa and receives
// asynchronous events as tea.Msg values (see docs/architecture.md).
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Minimum usable terminal size.
const minWidth, minHeight = 60, 12

type focus int

const (
	focusList focus = iota
	focusInput
)

type connState int

const (
	connConnecting connState = iota
	connOnline
	connOffline
	connNotPaired
	connLoggedOut
)

// Model is the root TUI model.
type Model struct {
	ctx  context.Context // for commands; cancelled when the program exits
	deps Deps
	sub  *wa.Subscription

	width, height int
	focus         focus
	conn          connState
	notice        string // last error or hint, shown in the status line
	noticeIsErr   bool

	chats chatList
	conv  conversation
}

// New returns the root model. sub must be a subscription on deps.Client,
// registered after any store recorder so events are stored before the UI
// reloads from the store.
func New(ctx context.Context, deps Deps, sub *wa.Subscription) Model {
	m := Model{ctx: ctx, deps: deps, sub: sub, conv: newConversation(), conn: connConnecting}
	if !deps.Client.IsPaired() {
		m.conn = connNotPaired
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitEvent(m.ctx, m.sub), loadChats(m.ctx, m.deps.Store)}
	if m.conn == connConnecting {
		cmds = append(cmds, connect(m.ctx, m.deps.Client))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case eventMsg:
		return m.handleEvent(msg.evt)
	case chatsLoadedMsg:
		if msg.err != nil {
			m.setError("loading chats", msg.err)
			return m, nil
		}
		before := m.chats.selectedJID()
		m.chats.setChats(msg.chats)
		if c, ok := m.chats.selectedChat(); ok {
			m.conv.setChat(c)
			if c.JID != before || len(m.conv.msgs) == 0 {
				return m, loadMessages(m.ctx, m.deps.Store, c.JID)
			}
		}
		return m, nil
	case messagesLoadedMsg:
		if msg.err != nil {
			m.setError("loading messages", msg.err)
			return m, nil
		}
		if msg.chat == m.conv.chat.JID {
			m.conv.setMessages(msg.msgs)
		}
		return m, nil
	case connectResultMsg:
		switch {
		case errors.Is(msg.err, wa.ErrNotPaired):
			m.conn = connNotPaired
		case msg.err != nil:
			m.conn = connOffline
			m.setError("connecting", msg.err)
		}
		return m, nil
	case sendResultMsg:
		if msg.err != nil {
			m.setError("sending", msg.err)
		}
		return m, nil
	}

	// Anything else (e.g. cursor blink) goes to the focused input.
	if m.focus == focusInput {
		var cmd tea.Cmd
		m.conv.input, cmd = m.conv.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch key {
	case "pgup":
		m.conv.viewport.PageUp()
		return m, nil
	case "pgdown":
		m.conv.viewport.PageDown()
		return m, nil
	}

	if m.focus == focusInput {
		switch key {
		case "esc":
			m.focus = focusList
			m.conv.input.Blur()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.conv.input.Value())
			if text == "" || m.conv.chat.JID == "" {
				return m, nil
			}
			m.conv.input.Reset()
			m.notice = ""
			return m, send(m.ctx, m.deps.Client, m.conv.chat.JID, text)
		}
		// Only the focused input sees typing, so "q" here is just a letter.
		var cmd tea.Cmd
		m.conv.input, cmd = m.conv.input.Update(msg)
		return m, cmd
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		return m.selectChat(-1)
	case "down", "j":
		return m.selectChat(1)
	case "enter", "tab", "right", "l":
		if m.conv.chat.JID == "" {
			return m, nil
		}
		m.focus = focusInput
		return m, m.conv.input.Focus()
	}
	return m, nil
}

func (m Model) selectChat(delta int) (tea.Model, tea.Cmd) {
	if !m.chats.move(delta) {
		return m, nil
	}
	c, _ := m.chats.selectedChat()
	m.conv.setChat(c)
	return m, loadMessages(m.ctx, m.deps.Store, c.JID)
}

func (m Model) handleEvent(evt wa.Event) (tea.Model, tea.Cmd) {
	next := waitEvent(m.ctx, m.sub)
	switch v := evt.(type) {
	case wa.Connected:
		m.conn = connOnline
	case wa.Disconnected:
		m.conn = connOffline
	case wa.LoggedOut:
		m.conn = connLoggedOut
		m.notice, m.noticeIsErr = "Logged out ("+v.Reason+"). Run `wa-tui login -relink` to pair again.", true
	case wa.Message:
		// The recorder stored it before this event reached us; reload.
		cmds := []tea.Cmd{next, loadChats(m.ctx, m.deps.Store)}
		if v.Chat == m.conv.chat.JID {
			cmds = append(cmds, loadMessages(m.ctx, m.deps.Store, v.Chat))
		}
		return m, tea.Batch(cmds...)
	case wa.HistorySync:
		cmds := []tea.Cmd{next, loadChats(m.ctx, m.deps.Store)}
		if m.conv.chat.JID != "" {
			cmds = append(cmds, loadMessages(m.ctx, m.deps.Store, m.conv.chat.JID))
		}
		return m, tea.Batch(cmds...)
	}
	return m, next
}

func (m *Model) setError(what string, err error) {
	m.notice, m.noticeIsErr = fmt.Sprintf("%s: %v", what, err), true
	m.deps.Log.Error(what, "err", err)
}

// listWidth is the chat list's share of the screen.
func (m Model) listWidth() int {
	return min(max(m.width/3, 20), 36)
}

func (m *Model) layout() {
	m.conv.resize(m.width-m.listWidth()-1, m.height-1)
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "go-whassup"
	return v
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small (%dx%d); need at least %dx%d.", m.width, m.height, minWidth, minHeight)
	}
	mainH := m.height - 1
	left := m.chats.view(m.listWidth(), mainH, m.focus == focusList)
	sep := separatorStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", mainH), "\n"))
	right := m.conv.view(m.width-m.listWidth()-1, mainH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	return body + "\n" + m.statusLine()
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
		state = errorStyle.Render("○ not paired: run `wa-tui login`")
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
	hints := "↑↓ chats · enter write · q quit"
	if m.focus == focusInput {
		hints = "enter send · esc chats · pgup/pgdn scroll"
	}
	parts = append(parts, statusStyle.Render(hints))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(parts, "  "))
}

// Package ui holds the Bubble Tea models. It must never import whatsmeow;
// it talks to the protocol layer only through internal/wa and receives
// asynchronous events as tea.Msg values (see docs/architecture.md).
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Minimum usable terminal size.
const minWidth, minHeight = 60, 12

type screen int

const (
	screenMain screen = iota
	screenLogin
	screenSettings
)

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
	screen        screen
	showHelp      bool
	focus         focus
	conn          connState
	notice        string // last error or hint, shown in the status line
	noticeIsErr   bool
	trueColor     bool // terminal renders 24-bit color (inline images need it)

	chats    chatList
	conv     conversation
	login    login
	settings settings
	help     help.Model
}

// New returns the root model. sub must be a subscription on deps.Client,
// registered after any store recorder so events are stored before the UI
// reloads from the store.
func New(ctx context.Context, deps Deps, sub *wa.Subscription) Model {
	h := help.New()
	h.Styles.ShortKey, h.Styles.FullKey = statusStyle.Bold(true), senderStyle
	h.Styles.ShortDesc, h.Styles.ShortSeparator = statusStyle, statusStyle
	m := Model{ctx: ctx, deps: deps, sub: sub, conv: newConversation(), login: newLogin(), conn: connConnecting, help: h}
	if !deps.Client.IsPaired() {
		m.conn = connNotPaired
		m = m.openLogin("")
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
		m.help.SetWidth(msg.Width)
		m.layout()
		return m, nil
	case tea.ColorProfileMsg:
		m.trueColor = msg.Profile == colorprofile.TrueColor
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case eventMsg:
		return m.handleEvent(msg.evt)
	case pairCodeMsg, pairQRStartedMsg, logoutMsg:
		return m.loginResult(msg)
	case configSavedMsg:
		return m.settingsSaved(msg)
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

	// Anything else (e.g. cursor blink) goes to whichever input has focus.
	var cmd tea.Cmd
	switch {
	case m.screen == screenLogin && m.login.step == loginPhone:
		m.login.phone, cmd = m.login.phone.Update(msg)
	case m.screen == screenSettings && m.settings.editing:
		m.settings.input, cmd = m.settings.input.Update(msg)
	case m.screen == screenMain && m.focus == focusInput:
		m.conv.input, cmd = m.conv.input.Update(msg)
	}
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.ForceQuit) {
		m.deps.Client.CancelPairing()
		m, hide, _ := m.hideQRImage()
		return m, tea.Sequence(hide, tea.Quit)
	}
	if m.showHelp {
		m.showHelp = false // any key closes the overlay
		return m, nil
	}
	switch m.screen {
	case screenLogin:
		return m.loginKey(msg)
	case screenSettings:
		return m.settingsKey(msg)
	}

	switch {
	case key.Matches(msg, keys.PageUp):
		m.conv.viewport.PageUp()
		return m, nil
	case key.Matches(msg, keys.PageDown):
		m.conv.viewport.PageDown()
		return m, nil
	}

	if m.focus == focusInput {
		switch {
		case key.Matches(msg, keys.Blur):
			m.focus = focusList
			m.conv.input.Blur()
			return m, nil
		case key.Matches(msg, keys.Send):
			text := strings.TrimSpace(m.conv.input.Value())
			if text == "" || m.conv.chat.JID == "" {
				return m, nil
			}
			m.conv.input.Reset()
			m.notice = ""
			return m, send(m.ctx, m.deps.Client, m.conv.chat.JID, text)
		}
		// Only the focused input sees typing, so q , ? L are just characters.
		var cmd tea.Cmd
		m.conv.input, cmd = m.conv.input.Update(msg)
		return m, cmd
	}

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.showHelp = true
	case key.Matches(msg, keys.Login):
		return m.openLogin(""), nil
	case key.Matches(msg, keys.Settings):
		return m.openSettings(), nil
	case key.Matches(msg, keys.Up):
		return m.selectChat(-1)
	case key.Matches(msg, keys.Down):
		return m.selectChat(1)
	case key.Matches(msg, keys.Open):
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
	var loginCmd tea.Cmd
	if m.screen == screenLogin {
		var done bool
		if m, loginCmd, done = m.loginEvent(evt); done {
			m.screen = screenMain
			m.notice, m.noticeIsErr = "Linked. History will appear as your phone sends it.", false
		}
	}
	next = tea.Batch(next, loginCmd)
	switch v := evt.(type) {
	case wa.Connected:
		m.conn = connOnline
		return m, tea.Batch(next, loadChats(m.ctx, m.deps.Store))
	case wa.Disconnected:
		m.conn = connOffline
	case wa.LoggedOut:
		m.conn = connLoggedOut
		m = m.openLogin("Logged out by WhatsApp (" + v.Reason + "). Link again to keep receiving messages.")
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
	bodyH := m.height - 1
	var body string
	switch {
	case m.showHelp:
		body = m.helpView(bodyH)
	case m.screen == screenLogin:
		body = m.loginView(m.width, bodyH)
	case m.screen == screenSettings:
		body = m.settingsView(m.width, bodyH)
	default:
		left := m.chats.view(m.listWidth(), bodyH, m.focus == focusList)
		sep := separatorStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
		right := m.conv.view(m.width-m.listWidth()-1, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	}
	return body + "\n" + m.statusLine()
}

func (m Model) helpView(height int) string {
	content := paneTitleStyle.Render("Keys") + "\n\n" + m.help.FullHelpView(fullMainHelp) +
		"\n\n" + faintStyle.Render("q , ? and L only act from the chat list; in the message box they're typed. Press any key to close.")
	return lipgloss.NewStyle().Width(m.width).Height(height).MaxHeight(height).Padding(0, 1).Render(content)
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
		state = errorStyle.Render("○ not linked")
		if m.screen != screenLogin {
			state = errorStyle.Render("○ not linked: press L")
		}
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
	parts = append(parts, m.help.ShortHelpView(m.contextHelp()))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(parts, "  "))
}

// contextHelp returns the bindings that apply right now.
func (m Model) contextHelp() []key.Binding {
	switch {
	case m.showHelp:
		return nil
	case m.screen == screenLogin:
		switch m.login.step {
		case loginPhone, loginFailed:
			return formHelp
		case loginRequesting, loginCode, loginQR:
			return waitHelp
		case loginUnlinking, loginFinishing:
			return nil
		case loginConfirm:
			return []key.Binding{keys.Back}
		}
		return menuHelp
	case m.screen == screenSettings:
		if m.settings.editing {
			return formHelp
		}
		return settingsHelp
	case m.focus == focusInput:
		return inputHelp
	default:
		return listHelp
	}
}

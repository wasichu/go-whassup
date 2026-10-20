// Package ui holds the Bubble Tea models. It must never import whatsmeow;
// it talks to the protocol layer only through internal/wa and receives
// asynchronous events as tea.Msg values (see docs/architecture.md).
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	screenFinder
	screenPicker
	screenAttach
	screenInfo
	screenSearch
	screenBrowse
	screenEmoji
	screenSender
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

	chats     chatList
	conv      conversation
	login     login
	settings  settings
	configs   *configWriter // saves settings in order (settings.go)
	finder    finder
	picker    picker
	attach    attach
	info      info
	search    search
	browser   browser
	emojiPick emojiPage  // the "+" reaction search (emojipage.go)
	sender    senderCard // "i" while picking a message (sender.go)
	browseDir string     // last folder a file was picked from (browser)
	draft     string     // JID of an open chat that has no messages yet
	ownJID    string     // the linked account, for labelling the chat with yourself

	downloading map[string]bool // attachment downloads in flight, by message ID

	termFocused bool                 // terminal window has focus; reads only count while it does
	interacted  bool                 // a key was pressed; nothing is read on launch alone
	readSeq     int                  // generation of the pending read timer (see scheduleRead)
	notified    map[string]time.Time // last desktop notification per chat (see notify)
	encSeq      int                  // generation of the encryption change being waited for
	help        help.Model
}

// New returns the root model. sub must be a subscription on deps.Client,
// registered after any store recorder so events are stored before the UI
// reloads from the store.
func New(ctx context.Context, deps Deps, sub *wa.Subscription) Model {
	h := help.New()
	h.Styles.ShortKey, h.Styles.FullKey = statusStyle.Bold(true), senderStyle
	h.Styles.ShortDesc, h.Styles.ShortSeparator = statusStyle, statusStyle
	m := Model{ctx: ctx, deps: deps, sub: sub, configs: &configWriter{}, conv: newConversation(), login: newLogin(), conn: connConnecting, help: h, ownJID: deps.Client.OwnJID(), termFocused: true, downloading: map[string]bool{}, notified: map[string]time.Time{}}
	switch {
	case deps.Notice != "":
		m.notice = deps.Notice
	case deps.FocusHint != "":
		m.notice = deps.FocusHint
	case deps.Notify.Hint != "" && deps.Config.Notifications.Enabled:
		m.notice = deps.Notify.Hint
	}
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
	if m.deps.Encryption != nil {
		if done := m.deps.Encryption.Running(); done != nil { // resumed or started at launch
			cmds = append(cmds, waitEncryption(done, m.encSeq, m.deps.Config.Encryption.Enabled))
		}
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
	case tea.FocusMsg:
		m.termFocused = true
		clear(m.notified)       // you're back; the next message away notifies again
		cmd := m.scheduleRead() // evaluate before m is copied into the return
		return m, cmd
	case tea.BlurMsg:
		m.termFocused = false
		return m, nil
	case tea.ColorProfileMsg:
		m.trueColor = msg.Profile == colorprofile.TrueColor
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case eventMsg:
		return m.handleEvent(msg.evt)
	case readTickMsg:
		return m.readTick(msg)
	case encryptionDoneMsg:
		return m.encryptionDone(msg)
	case pairCodeMsg, pairQRStartedMsg, logoutMsg:
		return m.loginResult(msg)
	case configSavedMsg:
		return m.configSaved(msg)
	case mediaOpenedMsg:
		return m.mediaOpened(msg)
	case chatsLoadedMsg:
		return m.chatsLoaded(msg)
	case messagesLoadedMsg:
		return m.messagesLoaded(msg), nil
	case connectResultMsg:
		return m.connectResult(msg), nil
	case readDoneMsg, fileSentMsg, sendResultMsg:
		return m.actionDone(msg)
	}
	return m.forward(msg)
}

// forward sends anything else (a page's results, cursor blink) to the
// active page, or to whichever input has focus. Results for a page that
// has closed are dropped.
func (m Model) forward(msg tea.Msg) (Model, tea.Cmd) {
	if m, cmd, ok := m.updatePage(msg); ok {
		return m, cmd
	}
	var cmd tea.Cmd
	switch {
	case m.screen == screenLogin && m.login.step == loginPhone:
		m.login.phone, cmd = m.login.phone.Update(msg)
	case m.screen == screenMain && m.focus == focusInput:
		m.conv.input, cmd = m.conv.input.Update(msg)
	}
	return m, cmd
}

// configSaved adopts a saved config (even if the settings page has closed
// meanwhile) and lets the page show the outcome.
func (m Model) configSaved(msg configSavedMsg) (Model, tea.Cmd) {
	if msg.superseded {
		return m, nil // a newer save was written and reports itself
	}
	var reload tea.Cmd
	if msg.saved {
		m.deps.Config = msg.cfg
		reload = loadChats(m.ctx, m.deps.Store) // retention may have pruned messages
	}
	var started tea.Cmd
	if msg.encryption != nil {
		started = m.encryptionStarted(msg.encryption, msg.encryptionOn)
	}
	m, cmd, _ := m.updatePage(msg)
	return m, tea.Batch(reload, started, cmd)
}

// actionDone reports the outcome of a send, file send or mark-read.
func (m Model) actionDone(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case readDoneMsg:
		if msg.err != nil {
			m.setError("marking read", msg.err)
		}
		return m, loadChats(m.ctx, m.deps.Store)
	case fileSentMsg:
		if msg.err != nil {
			m.setError("sending file", msg.err)
		} else {
			m.notice = ""
		}
	case sendResultMsg:
		if msg.err != nil {
			m.setError("sending", msg.err)
		}
	}
	return m, nil
}

// chatsLoaded refreshes the chat list, keeping the selection (or turning a
// draft into a regular chat once it's stored), and reloads the open chat
// when it changed.
func (m Model) chatsLoaded(msg chatsLoadedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.setError("loading chats", msg.err)
		return m, nil
	}
	before := m.chats.selectedJID()
	m.chats.setChats(msg.chats)
	if m.draft != "" {
		// A draft becomes a regular chat once its first message is stored.
		if !m.chats.selectJID(m.draft) {
			return m, nil
		}
		m.setDraft(nil)
		c, _ := m.chats.selectedChat()
		m.conv.setChat(c)
		cmd := tea.Batch(loadMessages(m.ctx, m.deps, c.JID), m.scheduleRead()) // evaluate before m is copied into the return
		return m, cmd
	}
	c, ok := m.chats.selectedChat()
	if !ok {
		return m, nil
	}
	m.conv.setChat(c)
	if c.JID != before || len(m.conv.msgs) == 0 {
		cmd := tea.Batch(loadMessages(m.ctx, m.deps, c.JID), m.scheduleRead()) // evaluate before m is copied into the return
		return m, cmd
	}
	if c.Unread > 0 {
		cmd := m.scheduleRead() // new messages arrived in the open chat; call before returning m
		return m, cmd
	}
	return m, nil
}

// messagesLoaded shows the open chat's messages (ignoring a load for a
// chat that's no longer open).
func (m Model) messagesLoaded(msg messagesLoadedMsg) Model {
	if msg.err != nil {
		m.setError("loading messages", msg.err)
		return m
	}
	if msg.chat != m.conv.chat.JID {
		return m
	}
	m.conv.cached, m.conv.mentions, m.conv.quotes, m.conv.own = msg.cached, msg.mentions, msg.quotes, m.ownJID
	m.conv.setMessages(msg.msgs)
	if h := m.conv.highlight; h != "" {
		if _, ok := m.conv.lineOf[h]; !ok {
			m.notice, m.noticeIsErr = fmt.Sprintf("That message is older than the %d loaded here.", messagesPerChat), false
			m.conv.highlight = ""
		}
	}
	return m
}

func (m Model) connectResult(msg connectResultMsg) Model {
	switch {
	case errors.Is(msg.err, wa.ErrNotPaired):
		m.conn = connNotPaired
	case msg.err != nil:
		m.conn = connOffline
		m.setError("connecting", msg.err)
	}
	return m
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.interacted = true
	if key.Matches(msg, keys.ForceQuit) {
		m.deps.Client.CancelPairing()
		m, hide, _ := m.hideQRImage()
		hidePreview := tea.Batch(m.browser.preview.hide(), m.picker.preview.hide())
		return m, tea.Sequence(hide, hidePreview, tea.Quit)
	}
	if m.showHelp {
		m.showHelp = false // any key closes the overlay
		return m, nil
	}
	if m.screen == screenLogin {
		return m.loginKey(msg)
	}
	if m, cmd, ok := m.updatePage(msg); ok {
		return m, cmd
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
		case m.conv.reacting:
			return m.reactKey(msg)
		case m.conv.picking:
			return m.pickKey(msg)
		}
		return m.inputKey(msg)
	}
	return m.listKey(msg)
}

// inputKey handles keys while the message input has focus. Only it sees
// typing, so q , ? L are just characters here.
func (m Model) inputKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Media):
		return m.openPicker()
	case key.Matches(msg, keys.Attach):
		return m.openAttach()
	case key.Matches(msg, keys.Pick):
		if m.draft == "" {
			m.conv.startPicking()
		}
		return m, nil
	case key.Matches(msg, keys.Blur) && m.conv.replyTo != nil:
		m.conv.setReply(nil) // esc cancels the reply before leaving the box
		return m, nil
	case key.Matches(msg, keys.Blur):
		m.focus = focusList
		m.conv.input.Blur()
		return m, nil
	case key.Matches(msg, keys.Send):
		return m.sendInput()
	}
	var cmd tea.Cmd
	m.conv.input, cmd = m.conv.input.Update(msg)
	return m, cmd
}

// sendInput sends what's typed: as a reply when one is being written.
func (m Model) sendInput() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.conv.input.Value())
	if text == "" || m.conv.chat.JID == "" {
		return m, nil
	}
	m.conv.input.Reset()
	m.notice = ""
	if to := m.conv.replyTo; to != nil {
		m.conv.setReply(nil)
		return m, sendReply(m.ctx, m.deps.Client, m.conv.chat.JID, text, *to)
	}
	return m, send(m.ctx, m.deps.Client, m.conv.chat.JID, text)
}

// listKey handles keys while the chat list has focus: navigation and the
// global shortcuts.
func (m Model) listKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.showHelp = true
	case key.Matches(msg, keys.Login):
		return m.openLogin(""), nil
	case key.Matches(msg, keys.Settings):
		return m.openSettings(), nil
	case key.Matches(msg, keys.Find):
		return m.openFinder()
	case key.Matches(msg, keys.Info):
		return m.openInfo()
	case key.Matches(msg, keys.Search):
		return m.openSearch()
	case key.Matches(msg, keys.Up):
		return m.selectChat(-1)
	case key.Matches(msg, keys.Down):
		return m.selectChat(1)
	case key.Matches(msg, keys.Open):
		if m.conv.chat.JID == "" {
			return m, nil
		}
		m.focus = focusInput
		cmd := m.conv.input.Focus() // evaluate before m is copied into the return
		return m, cmd
	}
	return m, nil
}

func (m Model) selectChat(delta int) (tea.Model, tea.Cmd) {
	switch {
	case m.draft != "":
		// The draft is the top row: up stays, down leaves it for the
		// first chat below (and the draft, never sent, goes away).
		if delta < 0 || len(m.chats.chats) == 0 {
			return m, nil
		}
		m.setDraft(nil)
		m.chats.selected = 0
	case !m.chats.move(delta):
		return m, nil
	}
	c, _ := m.chats.selectedChat()
	m.conv.setChat(c)
	cmd := tea.Batch(loadMessages(m.ctx, m.deps, c.JID), m.scheduleRead()) // evaluate before m is copied into the return
	return m, cmd
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
		m.ownJID = m.deps.Client.OwnJID() // may be new after (re-)linking
		return m, tea.Batch(next, loadChats(m.ctx, m.deps.Store))
	case wa.Disconnected:
		m.conn = connOffline
	case wa.LoggedOut:
		m.conn = connLoggedOut
		m = m.openLogin("Logged out by WhatsApp (" + v.Reason + "). Link again to keep receiving messages.")
	case wa.Message:
		// The recorder stored it before this event reached us; reload.
		cmds := []tea.Cmd{next, loadChats(m.ctx, m.deps.Store), m.notify(v, time.Now())}
		if v.Chat == m.conv.chat.JID {
			cmds = append(cmds, loadMessages(m.ctx, m.deps, v.Chat))
		}
		return m, tea.Batch(cmds...)
	case wa.MessageEdited, wa.MessageDeleted, wa.Reaction:
		cmds := []tea.Cmd{next, loadChats(m.ctx, m.deps.Store)} // deletes change unread counts
		if chatOf(evt) == m.conv.chat.JID {
			cmds = append(cmds, loadMessages(m.ctx, m.deps, m.conv.chat.JID))
		}
		return m, tea.Batch(cmds...)
	case wa.ChatMuted:
		return m, tea.Batch(next, loadChats(m.ctx, m.deps.Store)) // badge colour
	case wa.HistorySync, wa.Contacts, wa.Groups:
		// New names or history: refresh titles and the open chat's senders.
		cmds := []tea.Cmd{next, loadChats(m.ctx, m.deps.Store)}
		if m.conv.chat.JID != "" {
			cmds = append(cmds, loadMessages(m.ctx, m.deps, m.conv.chat.JID))
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
	v.ReportFocus = true // reads only count while the terminal is focused
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
	case m.page() != nil:
		body = m.page().view(rootAPI{&m}, m.width, bodyH)
	default:
		left := m.chats.view(m.listWidth(), bodyH, m.focus == focusList, m.ownJID)
		sep := separatorStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
		right := m.conv.view(m.width-m.listWidth()-1, bodyH, m.ownJID)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	}
	return body + "\n" + m.statusLine()
}

// chatOf returns the chat an edit, delete or reaction applies to.
func chatOf(evt wa.Event) string {
	switch v := evt.(type) {
	case wa.MessageEdited:
		return v.Chat
	case wa.MessageDeleted:
		return v.Chat
	case wa.Reaction:
		return v.Chat
	}
	return ""
}

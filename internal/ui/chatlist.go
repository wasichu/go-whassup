package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
)

// chatList is the left pane: a scrolling, single-selection list of chats.
type chatList struct {
	chats    []store.ChatSummary
	selected int
	offset   int // first visible row
	// draft, while set, is a chat opened from the finder that has no
	// messages yet. It's shown as the top row, selected, marked "new".
	draft *store.ChatSummary
}

// setChats replaces the list, keeping the selection on the same chat.
func (l *chatList) setChats(chats []store.ChatSummary) {
	current := l.selectedJID()
	l.chats = chats
	l.selected = 0
	for i, c := range chats {
		if c.JID == current {
			l.selected = i
			break
		}
	}
}

func (l *chatList) selectedChat() (store.ChatSummary, bool) {
	if l.selected < 0 || l.selected >= len(l.chats) {
		return store.ChatSummary{}, false
	}
	return l.chats[l.selected], true
}

func (l *chatList) selectedJID() string {
	c, _ := l.selectedChat()
	return c.JID
}

// selectJID selects the chat with jid and reports whether it exists.
func (l *chatList) selectJID(jid string) bool {
	for i, c := range l.chats {
		if c.JID == jid {
			l.selected = i
			return true
		}
	}
	return false
}

// find returns the listed chat with jid.
func (l *chatList) find(jid string) (store.ChatSummary, bool) {
	for _, c := range l.chats {
		if c.JID == jid {
			return c, true
		}
	}
	return store.ChatSummary{}, false
}

// move changes the selection by delta, clamped, and reports whether it moved.
func (l *chatList) move(delta int) bool {
	next := min(max(l.selected+delta, 0), len(l.chats)-1)
	if next == l.selected || next < 0 {
		return false
	}
	l.selected = next
	return true
}

// view renders the list into width x height, scrolling to keep the
// selection visible. active marks the pane as focused.
func (l *chatList) view(width, height int, active bool, own string) string {
	rows := height - 1 // title line
	if rows < 1 {
		return ""
	}
	items, sel := l.chats, l.selected
	if l.draft != nil {
		items, sel = append([]store.ChatSummary{*l.draft}, l.chats...), 0
	}
	if sel < l.offset {
		l.offset = sel
	}
	if sel >= l.offset+rows {
		l.offset = sel - rows + 1
	}

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("Chats"))
	if len(items) == 0 {
		b.WriteString("\n" + faintStyle.Render("No chats yet"))
	}
	line := lipgloss.NewStyle().Width(width).MaxWidth(width)
	for i := l.offset; i < len(items) && i < l.offset+rows; i++ {
		c := items[i]
		isDraft := l.draft != nil && i == 0
		title := chatTitle(c, own)
		badge := ""
		switch {
		case isDraft:
			badge = " new "
		case c.Unread > 0:
			badge = fmt.Sprintf(" %d ", c.Unread)
		}
		// Truncate the title so an unread badge always fits on the row.
		room := max(width-1-lipgloss.Width(badge), 1)
		title = lipgloss.NewStyle().MaxWidth(room).Render(title)
		label := " " + title + strings.Repeat(" ", max(room-lipgloss.Width(title), 0))
		style := line
		switch {
		case i == sel && active:
			style = line.Inherit(activeSelStyle)
		case i == sel:
			style = line.Inherit(selectedStyle)
		case c.Unread > 0:
			style = line.Bold(true)
		}
		row := style.Render(label)
		if badge != "" {
			bs := unreadBadgeStyle
			if isDraft || c.Muted(time.Now()) {
				bs = mutedBadgeStyle // grey, like the phone
			}
			row = lipgloss.NewStyle().Width(width-lipgloss.Width(badge)).MaxWidth(width-lipgloss.Width(badge)).Render(row) + bs.Render(badge)
		}
		b.WriteString("\n" + row)
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String())
}

// chatTitle is how a chat is labelled everywhere in the UI: the store's
// title, marked "(You)" for the chat with yourself, as WhatsApp Web does.
func chatTitle(c store.ChatSummary, own string) string {
	if own != "" && c.JID == own {
		return c.Title() + " (You)"
	}
	return c.Title()
}

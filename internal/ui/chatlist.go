package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
)

// chatList is the left pane: a scrolling, single-selection list of chats.
type chatList struct {
	chats    []store.ChatSummary
	selected int
	offset   int // first visible row
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
func (l *chatList) view(width, height int, active bool) string {
	rows := height - 1 // title line
	if rows < 1 {
		return ""
	}
	if l.selected < l.offset {
		l.offset = l.selected
	}
	if l.selected >= l.offset+rows {
		l.offset = l.selected - rows + 1
	}

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("Chats"))
	if len(l.chats) == 0 {
		b.WriteString("\n" + faintStyle.Render("No chats yet"))
	}
	line := lipgloss.NewStyle().Width(width).MaxWidth(width)
	for i := l.offset; i < len(l.chats) && i < l.offset+rows; i++ {
		c := l.chats[i]
		label := " " + c.Title()
		style := line
		if i == l.selected {
			style = line.Inherit(selectedStyle)
			if active {
				style = line.Inherit(activeSelStyle)
			}
		}
		b.WriteString("\n" + style.Render(label))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String())
}

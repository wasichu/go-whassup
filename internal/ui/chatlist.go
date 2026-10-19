package ui

import (
	"fmt"
	"slices"
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
	l.chats = groupCommunities(chats)
	l.selected = 0
	for i, c := range l.chats {
		if c.JID == current {
			l.selected = i
			break
		}
	}
}

// groupCommunities keeps each community's groups together, where its most
// recently active group would be (chats arrive newest first), so they can
// be listed under one heading.
func groupCommunities(chats []store.ChatSummary) []store.ChatSummary {
	out := make([]store.ChatSummary, 0, len(chats))
	placed := map[string]bool{}
	for _, c := range chats {
		switch {
		case c.CommunityJID == "":
			out = append(out, c)
		case !placed[c.CommunityJID]:
			placed[c.CommunityJID] = true
			for _, sib := range chats {
				if sib.CommunityJID == c.CommunityJID {
					out = append(out, sib)
				}
			}
		}
	}
	return out
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

// listRow is a line of the list: a chat (an index into the items) or a
// community's heading, which can't be selected.
type listRow struct {
	item    int // -1 for a heading
	heading string
}

// rowsOf lays out items, with a heading above each community's groups.
func rowsOf(items []store.ChatSummary) []listRow {
	rows := make([]listRow, 0, len(items))
	for i, c := range items {
		if c.CommunityJID != "" && (i == 0 || items[i-1].CommunityJID != c.CommunityJID) {
			rows = append(rows, listRow{item: -1, heading: c.Community})
		}
		rows = append(rows, listRow{item: i})
	}
	return rows
}

// view renders the list into width x height, scrolling to keep the
// selection (and its community's heading) visible. active marks the pane
// as focused.
func (l *chatList) view(width, height int, active bool, own string) string {
	visible := height - 1 // title line
	if visible < 1 {
		return ""
	}
	items, sel := l.chats, l.selected
	if l.draft != nil {
		items, sel = append([]store.ChatSummary{*l.draft}, l.chats...), 0
	}
	rows := rowsOf(items)
	selRow := slices.IndexFunc(rows, func(r listRow) bool { return r.item == sel })
	top := selRow
	if selRow > 0 && rows[selRow-1].item < 0 {
		top-- // the heading too
	}
	if top >= 0 && top < l.offset {
		l.offset = top
	}
	if selRow >= l.offset+visible {
		l.offset = selRow - visible + 1
	}

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("Chats"))
	if len(items) == 0 {
		b.WriteString("\n" + faintStyle.Render("No chats yet"))
	}
	for _, r := range rows[min(l.offset, len(rows)):min(l.offset+visible, len(rows))] {
		if r.item < 0 {
			b.WriteString("\n" + communityStyle.Width(width).MaxWidth(width).Render(" "+r.heading))
			continue
		}
		state := rowState{selected: r.item == sel, active: active, draft: l.draft != nil && r.item == 0}
		b.WriteString("\n" + chatRow(items[r.item], state, width, own))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String())
}

// communityStyle is a community's heading in the chat list.
var communityStyle = faintStyle.Bold(true)

type rowState struct{ selected, active, draft bool }

// chatRow is one chat's line: its title (a community's group by its name
// within the community, indented under the heading) and an unread badge.
func chatRow(c store.ChatSummary, st rowState, width int, own string) string {
	title := chatTitle(c, own)
	indent := " "
	if c.CommunityJID != "" {
		title, indent = c.InCommunity, "   "
	}
	badge := ""
	switch {
	case st.draft:
		badge = " new "
	case c.Unread > 0:
		badge = fmt.Sprintf(" %d ", c.Unread)
	}
	// Truncate the title so an unread badge always fits on the row.
	room := max(width-lipgloss.Width(indent)-lipgloss.Width(badge), 1)
	title = lipgloss.NewStyle().MaxWidth(room).Render(title)
	label := indent + title + strings.Repeat(" ", max(room-lipgloss.Width(title), 0))
	line := lipgloss.NewStyle().Width(width).MaxWidth(width)
	style := line
	switch {
	case st.selected && st.active:
		style = line.Inherit(activeSelStyle)
	case st.selected:
		style = line.Inherit(selectedStyle)
	case c.Unread > 0:
		style = line.Bold(true)
	}
	row := style.Render(label)
	if badge == "" {
		return row
	}
	bs := unreadBadgeStyle
	if st.draft || c.Muted(time.Now()) {
		bs = mutedBadgeStyle // grey, like the phone
	}
	rest := width - lipgloss.Width(badge)
	return lipgloss.NewStyle().Width(rest).MaxWidth(rest).Render(row) + bs.Render(badge)
}

// chatTitle is how a chat is labelled everywhere in the UI: the store's
// title, marked "(You)" for the chat with yourself, as WhatsApp Web does.
func chatTitle(c store.ChatSummary, own string) string {
	if own != "" && c.JID == own {
		return strings.TrimPrefix(c.Title(), "~") + " (You)" // your own name, no "~"
	}
	return c.Title()
}

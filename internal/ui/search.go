package ui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
)

// searchLimit caps results; refine the query to find older ones.
const searchLimit = 200

// search is the "s" screen: full-text search over stored messages.
type search struct {
	input    textinput.Model
	hits     []store.SearchHit
	selected int
	seq      int    // generation of the latest query; older results are dropped
	err      string // e.g. unavailable while encrypted
	searched bool
}

type searchResultsMsg struct {
	seq  int
	hits []store.SearchHit
	err  error
}

func runSearch(ctx context.Context, st Store, query string, seq int) tea.Cmd {
	return func() tea.Msg {
		hits, err := st.Search(ctx, query, searchLimit)
		return searchResultsMsg{seq: seq, hits: hits, err: err}
	}
}

func (m Model) openSearch() (Model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = "Search: "
	in.Placeholder = "words in messages"
	in.SetVirtualCursor(true)
	in.CharLimit = 200
	in.SetWidth(max(m.width-12, 10))
	m.screen, m.search = screenSearch, search{input: in}
	cmd := m.search.input.Focus() // evaluate before m is copied into the return
	return m, cmd
}

func (m Model) searchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.search
	switch {
	case key.Matches(msg, keys.Back):
		m.screen = screenMain
		return m, nil
	case msg.String() == "up", msg.String() == "ctrl+p":
		s.selected = max(s.selected-1, 0)
		return m, nil
	case msg.String() == "down", msg.String() == "ctrl+n":
		s.selected = min(s.selected+1, max(len(s.hits)-1, 0))
		return m, nil
	case msg.String() == "enter":
		if s.selected >= len(s.hits) {
			return m, nil
		}
		hit := s.hits[s.selected]
		m, cmd := m.openChat(hit.Chat)
		m.conv.highlight = hit.Message.ID
		m.focus = focusList // reading, not replying
		m.conv.input.Blur()
		if m.conv.showMessage(hit.Message.ID) { // already loaded (same chat)
			m.conv.render(false)
			m.conv.showMessage(hit.Message.ID)
		}
		return m, cmd
	}
	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() == before {
		return m, cmd
	}
	s.seq++
	s.selected, s.err = 0, ""
	if strings.TrimSpace(s.input.Value()) == "" {
		s.hits, s.searched = nil, false
		return m, cmd
	}
	return m, tea.Batch(cmd, runSearch(m.ctx, m.deps.Store, s.input.Value(), s.seq))
}

func (m Model) searchResults(msg searchResultsMsg) (Model, tea.Cmd) {
	if msg.seq != m.search.seq {
		return m, nil // results for an older query
	}
	m.search.searched = true
	switch {
	case errors.Is(msg.err, store.ErrSearchUnavailable):
		m.search.err, m.search.hits = msg.err.Error()+".", nil
	case msg.err != nil:
		m.search.err, m.search.hits = "search failed: "+msg.err.Error(), nil
	default:
		m.search.hits = msg.hits
	}
	return m, nil
}

// renderSnippet styles the matched words in a search snippet.
func renderSnippet(s string) string {
	var b strings.Builder
	for {
		start := strings.Index(s, store.MatchStart)
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], store.MatchEnd)
		if end < 0 {
			b.WriteString(strings.ReplaceAll(s, store.MatchStart, ""))
			return b.String()
		}
		b.WriteString(s[:start])
		b.WriteString(matchStyle.Render(s[start+len(store.MatchStart) : start+end]))
		s = s[start+end+len(store.MatchEnd):]
	}
}

func (m Model) searchView(width, height int) string {
	s := m.search
	lines := []string{paneTitleStyle.Render("Search messages"), "", s.input.View(), ""}
	switch {
	case s.err != "":
		lines = append(lines, errorStyle.Render(s.err))
	case s.searched && len(s.hits) == 0:
		lines = append(lines, faintStyle.Render("No messages match."))
	case !s.searched:
		lines = append(lines, faintStyle.Render("Searches messages stored on this computer. All words must match; the last can be a prefix."))
	}
	rows := max(height-len(lines)-1, 1)
	start := 0
	if s.selected >= rows {
		start = s.selected - rows + 1
	}
	for i := start; i < len(s.hits) && i < start+rows; i++ {
		h := s.hits[i]
		who := "me"
		if !h.Message.FromMe {
			who = senderName(h.Message, h.Chat)
		}
		meta := chatTitle(h.Chat, m.ownJID) + " · " + h.Message.Timestamp.Local().Format("Jan 2 15:04")
		line := faintStyle.Render(meta) + "  " + who + ": " + renderSnippet(h.Snippet)
		if i == s.selected {
			line = activeSelStyle.Render("›") + " " + line
		} else {
			line = "  " + line
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width-2).Render(line))
	}
	if len(s.hits) == searchLimit {
		lines = append(lines, faintStyle.Render("Showing the newest results; add words to narrow down."))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

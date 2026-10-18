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

func (s *search) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case searchResultsMsg:
		s.results(msg)
		return false, nil
	case tea.KeyPressMsg:
		return s.key(r, msg)
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return false, cmd
}

func (*search) help() []key.Binding { return finderHelp }

func (s *search) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		return true, nil
	case msg.String() == "up", msg.String() == "ctrl+p":
		s.selected = max(s.selected-1, 0)
		return false, nil
	case msg.String() == "down", msg.String() == "ctrl+n":
		s.selected = min(s.selected+1, max(len(s.hits)-1, 0))
		return false, nil
	case msg.String() == "enter":
		if s.selected >= len(s.hits) {
			return false, nil
		}
		return true, r.jumpTo(s.hits[s.selected])
	}
	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() == before {
		return false, cmd
	}
	s.seq++
	s.selected, s.err = 0, ""
	if strings.TrimSpace(s.input.Value()) == "" {
		s.hits, s.searched = nil, false
		return false, cmd
	}
	return false, tea.Batch(cmd, runSearch(r.ctx(), r.deps().Store, s.input.Value(), s.seq))
}

// jumpTo opens hit's chat with the message highlighted and in view, for
// reading rather than replying.
func (m Model) jumpTo(hit store.SearchHit) (Model, tea.Cmd) {
	m, cmd := m.openChat(hit.Chat)
	m.conv.highlight = hit.Message.ID
	m.focus = focusList
	m.conv.input.Blur()
	if m.conv.showMessage(hit.Message.ID) { // already loaded (same chat)
		m.conv.render(false)
		m.conv.showMessage(hit.Message.ID)
	}
	return m, cmd
}

func (s *search) results(msg searchResultsMsg) {
	if msg.seq != s.seq {
		return // results for an older query
	}
	s.searched = true
	switch {
	case errors.Is(msg.err, store.ErrSearchUnavailable):
		s.err, s.hits = msg.err.Error()+".", nil
	case msg.err != nil:
		s.err, s.hits = "search failed: "+msg.err.Error(), nil
	default:
		s.hits = msg.hits
	}
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

func (s *search) view(r rootAPI, width, height int) string {
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
		meta := chatTitle(h.Chat, r.own()) + " · " + h.Message.Timestamp.Local().Format("Jan 2 15:04")
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
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

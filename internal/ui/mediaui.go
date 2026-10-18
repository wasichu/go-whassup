package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/wa"
)

// picker is the "o" screen: the open chat's attachments, newest first.
type picker struct {
	items    []wa.Message
	selected int
}

// attach is the "a" screen: a file path and an optional caption.
type attach struct {
	path, caption textinput.Model
	complete      pathCompleter // tab-completes path
	onCaption     bool
	err           string
}

type (
	mediaOpenedMsg struct {
		chat, id, path string
		err            error
	}
	fileSentMsg struct{ err error }
)

func (m Model) openPicker() (Model, tea.Cmd) {
	var items []wa.Message
	for i := len(m.conv.msgs) - 1; i >= 0; i-- {
		if msg := m.conv.msgs[i]; msg.Media != nil && !msg.Deleted {
			items = append(items, msg)
		}
	}
	if len(items) == 0 {
		m.notice, m.noticeIsErr = "No attachments in this chat.", false
		return m, nil
	}
	m.screen, m.picker = screenPicker, picker{items: items}
	return m, nil
}

func (p *picker) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	switch {
	case key.Matches(k, keys.Back):
		return true, nil
	case key.Matches(k, keys.Up):
		p.selected = max(p.selected-1, 0)
	case key.Matches(k, keys.Down):
		p.selected = min(p.selected+1, len(p.items)-1)
	case key.Matches(k, keys.Select):
		return false, r.download(p.items[p.selected])
	}
	return false, nil
}

func (*picker) help() []key.Binding { return menuHelp }

// download fetches (if not cached) and opens an attachment of the open
// chat; the result arrives as mediaOpenedMsg, even if the picker has
// closed by then.
func (m *Model) download(item wa.Message) tea.Cmd {
	if m.downloading[item.ID] {
		return nil // already on its way
	}
	if m.deps.Media == nil || m.deps.OpenFile == nil {
		m.setError("opening", errors.New("media isn't available in this mode"))
		return nil
	}
	m.downloading[item.ID] = true
	m.notice, m.noticeIsErr = "Downloading "+mediaLabel(item)+"…", false
	return fetchAndOpen(m.ctx, m.deps, m.conv.chat.JID, item)
}

// fetchAndOpen downloads an attachment if it isn't cached, then opens it.
func fetchAndOpen(ctx context.Context, deps Deps, chat string, msg wa.Message) tea.Cmd {
	return func() tea.Msg {
		path, err := deps.Media.Fetch(ctx, deps.Client, chat, msg.ID, *msg.Media)
		if err == nil {
			err = deps.OpenFile(path)
		}
		return mediaOpenedMsg{chat: chat, id: msg.ID, path: path, err: err}
	}
}

func (m Model) mediaOpened(msg mediaOpenedMsg) (Model, tea.Cmd) {
	delete(m.downloading, msg.id)
	switch {
	case errors.Is(msg.err, wa.ErrMediaExpired):
		m.notice, m.noticeIsErr = "That file is no longer on WhatsApp's servers; ask the sender to send it again.", false
	case errors.Is(msg.err, media.ErrNotOpenable):
		// Lead with the outcome: the status line truncates long notices.
		m.notice, m.noticeIsErr = "Not opened (unrecognised file type); saved to "+tildePath(msg.path), false
	case msg.err != nil:
		m.setError("opening attachment", msg.err)
	default:
		m.notice, m.noticeIsErr = "Opened "+filepath.Base(msg.path), false
	}
	if msg.chat == m.conv.chat.JID {
		return m, loadMessages(m.ctx, m.deps, msg.chat) // refresh the ✓
	}
	return m, nil
}

func mediaLabel(msg wa.Message) string {
	if msg.Media.FileName != "" {
		return msg.Media.FileName
	}
	return msg.Kind
}

func (p *picker) view(r rootAPI, width, height int) string {
	lines := []string{paneTitleStyle.Render("Attachments in " + chatTitle(r.chat(), r.own())), ""}
	rows := max(height-len(lines)-1, 1)
	start := 0
	if p.selected >= rows {
		start = p.selected - rows + 1
	}
	for i := start; i < len(p.items) && i < start+rows; i++ {
		it := p.items[i]
		label := mediaLabel(it)
		if it.Kind != wa.KindDocument && it.Text != "" {
			label += ": " + it.Text
		}
		meta := it.Timestamp.Local().Format("Jan 2 15:04")
		if s := humanSize(it.Media.Size); s != "" {
			meta += " · " + s
		}
		if r.cached(it.ID) {
			meta += " · ✓"
		}
		if r.downloading(it.ID) {
			meta += " · downloading…"
		}
		line := "  " + label
		if i == p.selected {
			line = activeSelStyle.Render("› " + label)
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width-2).Render(line+"  "+faintStyle.Render(meta)))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

func newAttach(width int) attach {
	path := textinput.New()
	path.Prompt = "File:    "
	path.Placeholder = "~/Pictures/photo.jpg"
	path.SetVirtualCursor(true)
	path.CharLimit = 1024
	path.SetWidth(max(width-14, 10))
	path.ShowSuggestions = true
	path.KeyMap.AcceptSuggestion.SetEnabled(false) // tab is pathCompleter.complete
	caption := textinput.New()
	caption.Prompt = "Caption: "
	caption.Placeholder = "optional"
	caption.SetVirtualCursor(true)
	caption.CharLimit = 1024
	caption.SetWidth(max(width-14, 10))
	return attach{path: path, caption: caption}
}

func (m Model) openAttach() (Model, tea.Cmd) {
	if m.conv.chat.JID == "" {
		return m, nil
	}
	m.screen, m.attach = screenAttach, newAttach(m.width)
	cmd := m.attach.path.Focus() // evaluate before m is copied into the return
	return m, cmd
}

func (a *attach) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return a.key(r, msg)
	case dirListedMsg:
		a.complete.listedDir(&a.path, msg)
		return false, nil
	}
	var cmd tea.Cmd
	if a.onCaption {
		a.caption, cmd = a.caption.Update(msg)
	} else {
		a.path, cmd = a.path.Update(msg)
	}
	return false, cmd
}

func (*attach) help() []key.Binding { return attachHelp }

func (a *attach) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "tab":
		if !a.onCaption && a.complete.complete(&a.path) {
			a.err = ""
			return false, a.complete.refresh(&a.path)
		}
		return false, a.toggleField()
	case "shift+tab":
		return false, a.toggleField()
	case "enter":
		return a.send(r)
	}
	var cmd tea.Cmd
	if a.onCaption {
		a.caption, cmd = a.caption.Update(msg)
		return false, cmd
	}
	before := a.path.Value()
	a.path, cmd = a.path.Update(msg)
	if a.path.Value() == before {
		return false, cmd // e.g. ↑/↓ choosing a match
	}
	a.err = ""
	return false, tea.Batch(cmd, a.complete.refresh(&a.path))
}

// toggleField moves between the path and the caption.
func (a *attach) toggleField() tea.Cmd {
	a.onCaption = !a.onCaption
	if a.onCaption {
		a.path.Blur()
		return a.caption.Focus()
	}
	a.caption.Blur()
	return a.path.Focus()
}

// send checks the path names a file and sends it with the caption.
func (a *attach) send(r rootAPI) (bool, tea.Cmd) {
	path, err := expandHome(strings.TrimSpace(a.path.Value()))
	var fi os.FileInfo
	if err == nil {
		fi, err = os.Stat(path)
	}
	switch {
	case err != nil || path == "":
		a.err = "No such file: " + a.path.Value()
		return false, nil
	case fi.IsDir():
		a.err = "That's a folder; pick a file in it (tab completes)."
		return false, nil
	}
	r.notify("Sending " + filepath.Base(path) + "…")
	return true, sendFile(r.ctx(), r.deps(), r.chat().JID, path, strings.TrimSpace(a.caption.Value()))
}

// sendFile sends the file and caches a copy under the new message's ID, so
// the user's own attachment opens without a download.
func sendFile(ctx context.Context, deps Deps, chat, path, caption string) tea.Cmd {
	return func() tea.Msg {
		id, err := deps.Client.SendFile(ctx, chat, path, caption)
		if err == nil && deps.Media != nil {
			// The cached copy only saves a download; failing to make it
			// isn't a failed send.
			if cerr := deps.Media.Put(chat, id, path, wa.Media{FileName: filepath.Base(path)}); cerr != nil {
				deps.Log.Warn("caching sent file", "err", cerr)
			}
		}
		return fileSentMsg{err: err}
	}
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding home: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

func (a *attach) view(r rootAPI, width, height int) string {
	lines := []string{
		paneTitleStyle.Render("Send a file to " + chatTitle(r.chat(), r.own())), "",
		a.path.View(),
	}
	lines = append(lines, a.matchLines()...)
	lines = append(lines, a.caption.View(), "",
		faintStyle.Render(fmt.Sprintf("JPEG, PNG and WebP go as photos; anything else as a document (max %d MB).", wa.MaxSendSize>>20)),
	)
	if a.err != "" {
		lines = append(lines, "", errorStyle.Render(a.err))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

// maxMatchLines is how many path completions the attach page lists.
const maxMatchLines = 6

// matchLines lists the path's completions (names only), the highlighted
// one marked, when there's a choice to make.
func (a *attach) matchLines() []string {
	matches := a.path.MatchedSuggestions()
	if a.onCaption || len(matches) < 2 {
		return nil
	}
	cur := a.path.CurrentSuggestionIndex()
	start := min(max(cur-maxMatchLines/2, 0), max(len(matches)-maxMatchLines, 0))
	var out []string
	for i := start; i < len(matches) && i < start+maxMatchLines; i++ {
		_, name := splitPath(strings.TrimSuffix(matches[i], "/"))
		if strings.HasSuffix(matches[i], "/") {
			name += "/"
		}
		if i == cur {
			out = append(out, "         "+activeSelStyle.Render("› "+name))
		} else {
			out = append(out, "           "+faintStyle.Render(name))
		}
	}
	if n := len(matches) - maxMatchLines; n > 0 {
		out = append(out, "           "+faintStyle.Render(fmt.Sprintf("… %d matches (keep typing, or ↑/↓)", len(matches))))
	}
	return out
}

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

func (m Model) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.picker
	switch {
	case key.Matches(msg, keys.Back):
		m.screen = screenMain
	case key.Matches(msg, keys.Up):
		p.selected = max(p.selected-1, 0)
	case key.Matches(msg, keys.Down):
		p.selected = min(p.selected+1, len(p.items)-1)
	case key.Matches(msg, keys.Select):
		item := p.items[p.selected]
		if m.downloading[item.ID] {
			return m, nil // already on its way
		}
		if m.deps.Media == nil || m.deps.OpenFile == nil {
			m.setError("opening", errors.New("media isn't available in this mode"))
			return m, nil
		}
		m.downloading[item.ID] = true
		m.notice, m.noticeIsErr = "Downloading "+mediaLabel(item)+"…", false
		return m, fetchAndOpen(m.ctx, m.deps, m.conv.chat.JID, item)
	}
	return m, nil
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

func (m Model) pickerView(width, height int) string {
	p := m.picker
	lines := []string{paneTitleStyle.Render("Attachments in " + chatTitle(m.conv.chat, m.ownJID)), ""}
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
		if m.conv.cached[it.ID] {
			meta += " · ✓"
		}
		if m.downloading[it.ID] {
			meta += " · downloading…"
		}
		line := "  " + label
		if i == p.selected {
			line = activeSelStyle.Render("› " + label)
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width-2).Render(line+"  "+faintStyle.Render(meta)))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func newAttach(width int) attach {
	path := textinput.New()
	path.Prompt = "File:    "
	path.Placeholder = "~/Pictures/photo.jpg"
	path.SetVirtualCursor(true)
	path.CharLimit = 1024
	path.SetWidth(max(width-14, 10))
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

func (m Model) attachKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := &m.attach
	switch msg.String() {
	case "esc":
		m.screen = screenMain
		return m, nil
	case "tab", "shift+tab":
		a.onCaption = !a.onCaption
		if a.onCaption {
			a.path.Blur()
			cmd := a.caption.Focus()
			return m, cmd
		}
		a.caption.Blur()
		cmd := a.path.Focus()
		return m, cmd
	case "enter":
		path, err := expandHome(strings.TrimSpace(a.path.Value()))
		if err == nil {
			_, err = os.Stat(path)
		}
		if err != nil || path == "" {
			a.err = "No such file: " + a.path.Value()
			return m, nil
		}
		m.screen = screenMain
		m.notice, m.noticeIsErr = "Sending "+filepath.Base(path)+"…", false
		return m, sendFile(m.ctx, m.deps, m.conv.chat.JID, path, strings.TrimSpace(a.caption.Value()))
	}
	var cmd tea.Cmd
	if a.onCaption {
		a.caption, cmd = a.caption.Update(msg)
	} else {
		a.path, cmd = a.path.Update(msg)
		a.err = ""
	}
	return m, cmd
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

func (m Model) attachView(width, height int) string {
	a := m.attach
	lines := []string{
		paneTitleStyle.Render("Send a file to " + chatTitle(m.conv.chat, m.ownJID)), "",
		a.path.View(), a.caption.View(), "",
		faintStyle.Render(fmt.Sprintf("JPEG, PNG and WebP go as photos; anything else as a document (max %d MB).", wa.MaxSendSize>>20)),
	}
	if a.err != "" {
		lines = append(lines, "", errorStyle.Render(a.err))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

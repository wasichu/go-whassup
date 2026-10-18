package ui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/wa"
)

// picker is the "o" screen: the open chat's attachments, newest first.
type picker struct {
	items    []wa.Message
	selected int
	preview  previewer // keyed by message ID
}

type (
	// mediaOpenedMsg reports an attachment fetched and opened, or (save)
	// fetched and copied to the save folder (path is the copy).
	mediaOpenedMsg struct {
		chat, id, path string
		save           bool
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
	m.screen, m.picker = screenPicker, picker{items: items, preview: previewer{owner: "picker", tmux: m.deps.Graphics.Tmux}}
	cmd := m.picker.highlightChanged(m.inlineImages()) // evaluate before m is copied into the return
	return m, cmd
}

func (p *picker) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case previewTickMsg:
		return false, p.startPreview(r, msg)
	case previewReadyMsg:
		return false, p.preview.ready(msg)
	case tea.KeyPressMsg:
		return p.key(r, msg)
	}
	return false, nil
}

func (p *picker) key(r rootAPI, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(k, keys.Back):
		r.notify("") // what it said was about this view's attachments
		return true, p.preview.hide()
	case key.Matches(k, keys.Up):
		return false, p.move(r, -1)
	case key.Matches(k, keys.Down):
		return false, p.move(r, 1)
	case key.Matches(k, keys.Select):
		return false, r.openAttachment(p.items[p.selected])
	case key.Matches(k, saveKey):
		return false, r.saveAttachment(p.items[p.selected])
	}
	return false, nil
}

func (p *picker) move(r rootAPI, delta int) tea.Cmd {
	next := min(max(p.selected+delta, 0), len(p.items)-1)
	if next == p.selected {
		return nil
	}
	p.selected = next
	r.notify("") // an outcome ("File opened.") was about the previous attachment
	return p.highlightChanged(r.inlineImages())
}

// highlightChanged schedules a preview of the highlighted attachment: a
// photo (from the download, if any) or anything with WhatsApp's thumbnail.
func (p *picker) highlightChanged(images bool) tea.Cmd {
	it := p.items[p.selected]
	return p.preview.moved(images && (it.Media.Thumbnail != nil || it.Kind == wa.KindImage))
}

func (p *picker) startPreview(r rootAPI, msg previewTickMsg) tea.Cmd {
	if !p.preview.due(msg) {
		return nil
	}
	it, chat, cache := p.items[p.selected], r.chat().JID, r.deps().Media
	w, h := r.size()
	_, cols, rows := splitLayout(w, h-1)
	return p.preview.load(it.ID, cols, rows, func(cols, rows int) ([]byte, error) {
		return attachmentPreview(cache, chat, it, cols, rows)
	})
}

// attachmentPreview previews a downloaded photo at full quality, else
// WhatsApp's embedded thumbnail.
func attachmentPreview(cache MediaCache, chat string, it wa.Message, cols, rows int) ([]byte, error) {
	if cache != nil {
		if path, ok := cache.Lookup(chat, it.ID); ok && canPreview(path) {
			if png, err := previewPNG(path, cols, rows); err == nil {
				return png, nil
			}
		}
	}
	return previewBytes(it.Media.Thumbnail, cols, rows)
}

func (*picker) help() []key.Binding { return pickerHelp }

// openAttachment fetches (if not cached) and opens an attachment of the open
// chat; the result arrives as mediaOpenedMsg, even if the picker has
// closed by then.
func (m *Model) openAttachment(item wa.Message) tea.Cmd {
	return m.fetch(item, "Downloading", fetchAndOpen)
}

// saveAttachment downloads an attachment if needed and copies it to the
// save folder; the result arrives as mediaOpenedMsg (save set).
func (m *Model) saveAttachment(item wa.Message) tea.Cmd {
	return m.fetch(item, "Saving", fetchAndSave)
}

// fetch starts then (fetchAndOpen or fetchAndSave) for an attachment of
// the open chat, once at a time per attachment.
func (m *Model) fetch(item wa.Message, verb string, then func(context.Context, Deps, string, wa.Message) tea.Cmd) tea.Cmd {
	if m.downloading[item.ID] {
		return nil // already on its way
	}
	if m.deps.Media == nil || m.deps.OpenFile == nil {
		m.setError("opening", errors.New("media isn't available in this mode"))
		return nil
	}
	m.downloading[item.ID] = true
	m.notice, m.noticeIsErr = verb+" "+mediaLabel(item)+"…", false
	return then(m.ctx, m.deps, m.conv.chat.JID, item)
}

// fetchAndSave downloads an attachment if it isn't cached, then copies it
// to the save folder (config [media] save_dir) under a readable name.
func fetchAndSave(ctx context.Context, deps Deps, chat string, msg wa.Message) tea.Cmd {
	return func() tea.Msg {
		out := mediaOpenedMsg{chat: chat, id: msg.ID, save: true}
		cached, err := deps.Media.Fetch(ctx, deps.Client, chat, msg.ID, *msg.Media)
		if err != nil {
			out.err = err
			return out
		}
		dir, err := expandHome(deps.Config.Media.SaveDir)
		if err == nil {
			out.path, err = media.SaveCopy(cached, dir, media.SaveName(*msg.Media, msg.Kind, msg.Timestamp))
		}
		out.err = err
		return out
	}
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
	case msg.err != nil && msg.save:
		m.setError("saving attachment", msg.err)
	case msg.save:
		m.notice, m.noticeIsErr = "Saved to "+tildePath(msg.path), false
		if m.deps.Config.Encryption.Enabled {
			m.notice += " (not encrypted)"
		}
	case errors.Is(msg.err, media.ErrNotOpenable):
		// The cached copy's path means nothing to the user (hashed name,
		// maybe a temporary folder); say what they can do instead.
		m.notice, m.noticeIsErr = "Not opened: unrecognised file type. Press d to save a copy.", false
	case msg.err != nil:
		m.setError("opening attachment", msg.err)
	default:
		// The cached file's name is a hash: nothing worth showing. And only
		// while the attachments view is still open: it's no news elsewhere.
		if m.screen == screenPicker {
			m.notice, m.noticeIsErr = "File opened.", false
		} else if m.notice != "" && !m.noticeIsErr {
			m.notice = "" // the "Downloading …" this answers
		}
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
	title := paneTitleStyle.Render("Attachments in " + chatTitle(r.chat(), r.own()))
	keysLine := "\n\n" + keyLine(pickerHelp)
	if !r.inlineImages() {
		return pageStyle(width, height).Render(title + "\n\n" + p.listView(r, width, height-5) + keysLine)
	}
	listW, _, _ := splitLayout(width, height)
	bodyH := max(height-5, 1)
	list := lipgloss.NewStyle().Width(listW).Height(bodyH).MaxHeight(bodyH).Render(p.listView(r, listW, bodyH))
	side := ""
	if it := p.items[p.selected]; p.preview.showing(it.ID) {
		side = lipgloss.NewStyle().PaddingLeft(2).Render(p.preview.placeholders())
	}
	return pageStyle(width, height).Render(title + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, list, side) + keysLine)
}

// listView lists the attachments, scrolled to keep the selection visible.
func (p *picker) listView(r rootAPI, width, height int) string {
	var lines []string
	rows := max(height, 1)
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
	return strings.Join(lines, "\n")
}

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
	preview  previewer // keyed by message ID
}

// attach is the "a" screen: a file path and an optional caption.
type attach struct {
	path, caption textinput.Model
	complete      pathCompleter // tab-completes path
	onCaption     bool
	err           string
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
		return true, p.preview.hide()
	case key.Matches(k, keys.Up):
		return false, p.move(r, -1)
	case key.Matches(k, keys.Down):
		return false, p.move(r, 1)
	case key.Matches(k, keys.Select):
		return false, r.download(p.items[p.selected], false)
	case key.Matches(k, saveKey):
		return false, r.download(p.items[p.selected], true)
	}
	return false, nil
}

func (p *picker) move(r rootAPI, delta int) tea.Cmd {
	next := min(max(p.selected+delta, 0), len(p.items)-1)
	if next == p.selected {
		return nil
	}
	p.selected = next
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
	_, cols, rows := browserLayout(w, h-1)
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

// download fetches (if not cached) and opens an attachment of the open
// chat; the result arrives as mediaOpenedMsg, even if the picker has
// closed by then.
func (m *Model) download(item wa.Message, save bool) tea.Cmd {
	if m.downloading[item.ID] {
		return nil // already on its way
	}
	if m.deps.Media == nil || m.deps.OpenFile == nil {
		m.setError("opening", errors.New("media isn't available in this mode"))
		return nil
	}
	m.downloading[item.ID] = true
	if save {
		m.notice, m.noticeIsErr = "Saving "+mediaLabel(item)+"…", false
		return fetchAndSave(m.ctx, m.deps, m.conv.chat.JID, item)
	}
	m.notice, m.noticeIsErr = "Downloading "+mediaLabel(item)+"…", false
	return fetchAndOpen(m.ctx, m.deps, m.conv.chat.JID, item)
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
	title := paneTitleStyle.Render("Attachments in " + chatTitle(r.chat(), r.own()))
	keysLine := "\n\n" + keyLine(pickerHelp)
	if !r.inlineImages() {
		return pageStyle(width, height).Render(title + "\n\n" + p.listView(r, width, height-5) + keysLine)
	}
	listW, _, _ := browserLayout(width, height)
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

// openAttach starts sending a file: the browser with previews when the
// terminal can show images, else the typed path with tab completion.
func (m Model) openAttach() (Model, tea.Cmd) {
	if m.conv.chat.JID == "" {
		return m, nil
	}
	if m.inlineImages() {
		return m.openBrowser()
	}
	m.screen, m.attach = screenAttach, newAttach(m.width)
	cmd := m.attach.path.Focus() // evaluate before m is copied into the return
	return m, cmd
}

// attachPath opens the send-file form with path filled in: a file picked
// in the browser (caption focused), or its folder to type from.
func (m Model) attachPath(path string, toCaption bool) (Model, tea.Cmd) {
	m.browseDir = filepath.Dir(path)
	if strings.HasSuffix(path, string(filepath.Separator)) {
		m.browseDir = filepath.Clean(path)
	}
	m.screen, m.attach = screenAttach, newAttach(m.width)
	m.attach.path.SetValue(path)
	m.attach.path.CursorEnd()
	if toCaption {
		m.attach.onCaption = true
		cmd := m.attach.caption.Focus() // evaluate before m is copied into the return
		return m, cmd
	}
	cmd := tea.Batch(m.attach.path.Focus(), m.attach.complete.refresh(&m.attach.path)) // evaluate before m is copied into the return
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

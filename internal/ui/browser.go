package ui

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// previewDelay is how long the cursor must rest on a file before its
// preview is decoded, so scrolling doesn't decode every image passed.
const previewDelay = 150 * time.Millisecond

// browser is the send-file screen when the terminal can show images: a
// directory listing with a preview of the highlighted image. Elsewhere,
// and on "/", the typed path with tab completion (attach) is used.
type browser struct {
	dir      string // absolute
	entries  []browseEntry
	loaded   bool
	err      string
	hidden   bool // show dotfiles
	selected int
	seq      int    // preview generation; older results are dropped
	shown    string // path whose preview is in the terminal, "" if none
	cols     int    // its size in cells
	rows     int
	tmux     bool
}

type browseEntry struct {
	name string
	dir  bool
	size int64
	mod  time.Time
}

type (
	browseListedMsg struct {
		dir     string
		entries []browseEntry
		err     error
	}
	previewTickMsg  struct{ seq int }
	previewReadyMsg struct {
		seq        int
		path       string
		png        []byte
		cols, rows int
		err        error
	}
)

// openBrowser shows the file browser in dir (the last one used, else home).
func (m Model) openBrowser() (Model, tea.Cmd) {
	dir := m.browseDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = home
	}
	m.screen, m.browser = screenBrowse, browser{dir: dir, tmux: m.deps.Graphics.Tmux}
	return m, listBrowse(dir)
}

func listBrowse(dir string) tea.Cmd {
	return func() tea.Msg {
		des, err := os.ReadDir(dir)
		msg := browseListedMsg{dir: dir, err: err}
		for _, de := range des[:min(len(des), maxDirEntries)] {
			e := browseEntry{name: de.Name(), dir: de.IsDir()}
			// Info, or Stat through a symlink: a failure just leaves the
			// size and date blank.
			if fi, err := os.Stat(filepath.Join(dir, de.Name())); err == nil {
				e.dir, e.size, e.mod = fi.IsDir(), fi.Size(), fi.ModTime()
			}
			msg.entries = append(msg.entries, e)
		}
		slices.SortFunc(msg.entries, func(a, b browseEntry) int {
			if a.dir != b.dir {
				if a.dir {
					return -1
				}
				return 1
			}
			return cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
		})
		return msg
	}
}

func loadPreview(seq int, path string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		png, err := previewPNG(path, cols, rows)
		return previewReadyMsg{seq: seq, path: path, png: png, cols: cols, rows: rows, err: err}
	}
}

// visible is the listing as shown (dotfiles only when asked for).
func (b *browser) visible() []browseEntry {
	if b.hidden {
		return b.entries
	}
	return slices.DeleteFunc(slices.Clone(b.entries), func(e browseEntry) bool { return strings.HasPrefix(e.name, ".") })
}

func (b *browser) current() (browseEntry, bool) {
	v := b.visible()
	if b.selected >= len(v) {
		return browseEntry{}, false
	}
	return v[b.selected], true
}

func (b *browser) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case browseListedMsg:
		if msg.dir != b.dir {
			return false, nil
		}
		b.entries, b.loaded, b.err, b.selected = msg.entries, true, "", 0
		if msg.err != nil {
			b.err = msg.err.Error()
		}
		return false, b.highlightChanged(r)
	case previewTickMsg:
		return false, b.startPreview(r, msg)
	case previewReadyMsg:
		return false, b.previewReady(msg)
	case tea.KeyPressMsg:
		return b.key(r, msg)
	}
	return false, nil
}

func (b *browser) key(r rootAPI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, b.hide()
	case "up", "k":
		return false, b.move(r, -1)
	case "down", "j":
		return false, b.move(r, 1)
	case "enter", "right", "l":
		e, ok := b.current()
		switch {
		case !ok:
			return false, nil
		case e.dir:
			return false, b.cd(filepath.Join(b.dir, e.name))
		}
		path := filepath.Join(b.dir, e.name)
		return false, tea.Batch(b.hide(), r.attachPath(path, true))
	case "left", "h", "backspace":
		return false, b.cd(filepath.Dir(b.dir))
	case ".":
		b.hidden, b.selected = !b.hidden, 0
		return false, b.highlightChanged(r)
	case "/", "tab":
		return false, tea.Batch(b.hide(), r.attachPath(b.dir+string(filepath.Separator), false))
	}
	return false, nil
}

func (b *browser) move(r rootAPI, delta int) tea.Cmd {
	next := min(max(b.selected+delta, 0), max(len(b.visible())-1, 0))
	if next == b.selected {
		return nil
	}
	b.selected = next
	return b.highlightChanged(r)
}

func (b *browser) cd(dir string) tea.Cmd {
	if dir == b.dir {
		return nil
	}
	b.dir, b.loaded, b.entries, b.selected = dir, false, nil, 0
	b.seq++
	return tea.Batch(b.hide(), listBrowse(dir))
}

// highlightChanged schedules a preview of the newly highlighted file, and
// drops the old one.
func (b *browser) highlightChanged(r rootAPI) tea.Cmd {
	b.seq++
	hide := b.hide()
	if e, ok := b.current(); !ok || e.dir || !canPreview(e.name) || !r.inlineImages() {
		return hide
	}
	seq := b.seq
	return tea.Batch(hide, tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewTickMsg{seq: seq} }))
}

func (b *browser) startPreview(r rootAPI, msg previewTickMsg) tea.Cmd {
	e, ok := b.current()
	if msg.seq != b.seq || !ok {
		return nil
	}
	w, h := r.size()
	_, cols, rows := browserLayout(w, h-1)
	return loadPreview(b.seq, filepath.Join(b.dir, e.name), cols, rows)
}

func (b *browser) previewReady(msg previewReadyMsg) tea.Cmd {
	if msg.seq != b.seq || msg.err != nil {
		return nil // superseded, or not previewable after all
	}
	b.shown, b.cols, b.rows = msg.path, msg.cols, msg.rows
	return tea.Raw(kittyTransmit(previewImageID, msg.png, msg.cols, msg.rows, b.tmux))
}

// hide frees the preview image, if one is in the terminal.
func (b *browser) hide() tea.Cmd {
	if b.shown == "" {
		return nil
	}
	b.shown = ""
	return tea.Raw(kittyDelete(previewImageID, b.tmux))
}

func (*browser) help() []key.Binding { return browserHelp }

// browserLayout splits the page: the listing's width, and the preview
// area in cells.
func browserLayout(width, height int) (listW, cols, rows int) {
	listW = min(max(width*2/5, 24), 50)
	cols = max(min(width-listW-4, 70), 4)
	rows = max(min(height-6, 35), 2)
	return listW, cols, rows
}

func (b *browser) view(r rootAPI, width, height int) string {
	listW, _, _ := browserLayout(width, height)
	head := []string{
		paneTitleStyle.Render("Send a file to " + chatTitle(r.chat(), r.own())),
		faintStyle.Render(tildePath(b.dir)), "",
	}
	bodyH := max(height-len(head)-1, 1)
	list := lipgloss.NewStyle().Width(listW).Height(bodyH).MaxHeight(bodyH).Render(b.listView(listW, bodyH))
	side := lipgloss.NewStyle().PaddingLeft(2).Render(b.sideView(width - listW - 4))
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, side)
	return pageStyle(width, height).Render(strings.Join(head, "\n") + "\n" + body)
}

func (b *browser) listView(width, height int) string {
	v := b.visible()
	switch {
	case b.err != "":
		return errorStyle.Render(b.err)
	case !b.loaded:
		return faintStyle.Render("Loading…")
	case len(v) == 0:
		return faintStyle.Render("Empty folder (. shows hidden files)")
	}
	start := min(max(b.selected-height/2, 0), max(len(v)-height, 0))
	var lines []string
	for i := start; i < len(v) && i < start+height; i++ {
		name := v[i].name
		if v[i].dir {
			name += "/"
		}
		name = lipgloss.NewStyle().MaxWidth(width - 2).Render(name)
		if i == b.selected {
			lines = append(lines, activeSelStyle.Render("› "+name))
		} else {
			lines = append(lines, "  "+name)
		}
	}
	return strings.Join(lines, "\n")
}

// sideView is the preview (when it's in the terminal) or the highlighted
// entry's details.
func (b *browser) sideView(width int) string {
	e, ok := b.current()
	if !ok {
		return ""
	}
	path := filepath.Join(b.dir, e.name)
	if b.shown == path {
		return kittyPlaceholders(previewImageID, b.cols, b.rows) + "\n" + faintStyle.Render(e.name+" · "+humanSize(e.size))
	}
	if e.dir {
		return faintStyle.Render("Folder · enter opens it")
	}
	lines := []string{senderStyle.Render(lipgloss.NewStyle().MaxWidth(width).Render(e.name))}
	meta := humanSize(e.size)
	if !e.mod.IsZero() {
		meta += " · " + e.mod.Local().Format("Jan 2 2006 15:04")
	}
	lines = append(lines, faintStyle.Render(meta), "", sendsAs(e.name))
	if canPreview(e.name) {
		lines = append(lines, "", faintStyle.Render("Loading preview…"))
	}
	return strings.Join(lines, "\n")
}

// sendsAs says how WhatsApp will show the file (see wa.SendFile).
func sendsAs(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return "Sends as a photo"
	}
	return "Sends as a document"
}

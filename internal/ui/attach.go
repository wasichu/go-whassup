package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// attach is the "a" screen: a file path and an optional caption.
type attach struct {
	path, caption textinput.Model
	complete      pathCompleter // tab-completes path
	onCaption     bool
	err           string
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

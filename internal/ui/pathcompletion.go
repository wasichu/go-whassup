package ui

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// maxDirEntries caps a directory listing used for completion.
const maxDirEntries = 2000

// pathCompleter completes file paths in a textinput, like a shell: it
// feeds the input's suggestions from the directory being typed (listed
// off the UI goroutine, once per directory). Hidden entries are offered
// only once the name being typed starts with ".".
type pathCompleter struct {
	listed  string // directory part of the value, as typed, that entries belong to
	have    bool   // listed has been requested
	entries []pathEntry
}

type pathEntry struct {
	name string
	dir  bool
}

// dirListedMsg carries a directory listing for completion.
type dirListedMsg struct {
	dir     string // as typed (the key, not the expanded path)
	entries []pathEntry
}

// splitPath splits a typed path into its directory part (up to and
// including the last "/") and the name being typed.
func splitPath(v string) (dir, base string) {
	i := strings.LastIndex(v, "/")
	return v[:i+1], v[i+1:]
}

// refresh updates in's suggestions for its current value. If the value
// is now in another directory, it returns the command that lists it.
func (c *pathCompleter) refresh(in *textinput.Model) tea.Cmd {
	dir, base := splitPath(in.Value())
	if !c.have || dir != c.listed {
		c.listed, c.have, c.entries = dir, true, nil
		in.SetSuggestions(nil)
		return listDir(dir)
	}
	c.suggest(in, dir, base)
	return nil
}

// listedDir takes a listing, if it's still for the directory being typed.
func (c *pathCompleter) listedDir(in *textinput.Model, msg dirListedMsg) {
	dir, base := splitPath(in.Value())
	if !c.have || msg.dir != c.listed || dir != c.listed {
		return
	}
	c.entries = msg.entries
	c.suggest(in, dir, base)
}

func (c *pathCompleter) suggest(in *textinput.Model, dir, base string) {
	hidden := strings.HasPrefix(base, ".")
	var out []string
	for _, e := range c.entries {
		if strings.HasPrefix(e.name, ".") && !hidden {
			continue
		}
		s := dir + e.name
		if e.dir {
			s += "/"
		}
		out = append(out, s)
	}
	in.SetSuggestions(out)
}

// complete is tab: extend the value to the longest prefix all matches
// share, or, if that adds nothing, take the highlighted match (↑/↓ choose).
// Matching ignores case, so the value takes the entry's real spelling.
// It reports whether the value changed.
func (c *pathCompleter) complete(in *textinput.Model) bool {
	matches := in.MatchedSuggestions()
	if len(matches) == 0 {
		return false
	}
	before := in.Value()
	next := commonPrefix(matches)
	if len([]rune(next)) < len([]rune(before)) || next == before {
		next = in.CurrentSuggestion() // nothing shared to add: take the highlighted one
	}
	if next == before {
		return false
	}
	in.SetValue(next)
	in.CursorEnd()
	return true
}

// commonPrefix is the longest prefix (in whole runes) all of ss share.
func commonPrefix(ss []string) string {
	p := []rune(ss[0])
	for _, s := range ss[1:] {
		r := []rune(s)
		n := 0
		for n < len(p) && n < len(r) && p[n] == r[n] {
			n++
		}
		p = p[:n]
	}
	return string(p)
}

// listDir lists a typed directory ("" = the current one; "~/" expands).
// Failures just mean no suggestions.
func listDir(typed string) tea.Cmd {
	return func() tea.Msg {
		msg := dirListedMsg{dir: typed}
		path := typed
		if path == "" {
			path = "."
		}
		path, err := expandHome(path)
		if err != nil {
			return msg
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return msg
		}
		for _, e := range entries[:min(len(entries), maxDirEntries)] {
			dir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 {
				if fi, err := os.Stat(filepath.Join(path, e.Name())); err == nil {
					dir = fi.IsDir()
				}
			}
			msg.entries = append(msg.entries, pathEntry{name: e.Name(), dir: dir})
		}
		return msg
	}
}

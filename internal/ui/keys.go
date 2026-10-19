package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// keys is the single source of truth for key bindings: handlers match
// against it and the status-line hints and ? overlay are generated from it.
var keys = struct {
	// Chat list (main screen, list focused).
	Up, Down, Open, Quit, Find, Search, Info, Login, Settings, Help key.Binding
	// Message input focused. Attachments use ctrl keys there: plain
	// letters are typed.
	Send, Blur, Media, Attach, Pick key.Binding
	// Main screen, either focus.
	PageUp, PageDown key.Binding
	// Lists on the login and settings screens.
	Select, Back, Submit, Cancel, Edit key.Binding
	// Everywhere.
	ForceQuit key.Binding
}{
	Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "previous chat")),
	Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "next chat")),
	Open:      key.NewBinding(key.WithKeys("enter", "tab", "right", "l"), key.WithHelp("enter", "write a message")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Find:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "find a chat or contact")),
	Search:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "search messages")),
	Media:     key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "view attachments")),
	Attach:    key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "attach file")),
	Pick:      key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reply or react")),
	Info:      key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "chat info")),
	Login:     key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "link or re-link")),
	Settings:  key.NewBinding(key.WithKeys(","), key.WithHelp(",", "settings")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "all keys")),
	Send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
	Blur:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to chats")),
	PageUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
	PageDown:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
	Select:    key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "select")),
	Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Edit:      key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "change")),
	Submit:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
	Cancel:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit, from anywhere")),
}

// Binding sets per context, rendered with help.ShortHelpView/FullHelpView.
var (
	// The status line shows a few essentials (short descriptions); ? shows
	// every key, by section (mainHelpSections).
	listHelp = []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "chats")),
		hint(keys.Open, "write"), hint(keys.Find, "find"), hint(keys.Search, "search"), keys.Help, keys.Quit,
	}
	inputHelp = []key.Binding{
		keys.Send, hint(keys.Blur, "chats"), hint(keys.Pick, "reply/react"), hint(keys.Media, "attachments"),
		hint(keys.Attach, "attach file"),
	}
	pickHelp = []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose a message")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reply")),
		key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "react")),
		key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "any emoji")),
		hint(keys.Blur, "back"),
	}
	reactHelp = []key.Binding{
		key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "choose")),
		key.NewBinding(key.WithKeys("1"), key.WithHelp("1-6", "pick")),
		key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "any emoji")),
		keys.Send,
		hint(keys.Blur, "back"),
	}
	menuHelp   = []key.Binding{keys.Up, keys.Down, keys.Select, keys.Back}
	formHelp   = []key.Binding{keys.Submit, keys.Back}
	waitHelp   = []key.Binding{keys.Cancel}
	attachHelp = []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose")),
		key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "caption")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		keys.Back,
	}
	finderHelp = []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		keys.Back,
	}
	// ←/→ cycle a choice either way, so every option is reachable without
	// passing through one that asks for more (key_source = password-command).
	settingsPrev = key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/→", "cycle"))
	settingsNext = key.NewBinding(key.WithKeys("right", "l"))
	browserHelp  = []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/pick")),
		key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "up a folder")),
		key.NewBinding(key.WithKeys("."), key.WithHelp(".", "hidden")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "type a path")),
		keys.Back,
	}
	saveKey          = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "save a copy"))
	pickerHelp       = []key.Binding{keys.Up, keys.Down, hint(keys.Select, "open"), saveKey, keys.Back}
	settingsHelp     = []key.Binding{keys.Up, keys.Down, keys.Edit, settingsPrev, keys.Back}
	mainHelpSections = []helpSection{
		{"Chat list", []key.Binding{keys.Up, keys.Down, keys.Open, keys.Find, keys.Search, keys.Info, keys.Login, keys.Settings, keys.Help, keys.Quit}},
		{"Writing a message", []key.Binding{keys.Send, keys.Blur, keys.Pick, keys.Media, keys.Attach, keys.PageUp, keys.PageDown}},
		{"Anywhere", []key.Binding{keys.ForceQuit}},
	}
)

// helpSection is a titled group of keys in the ? overlay.
type helpSection struct {
	title string
	keys  []key.Binding
}

// hint is b with a shorter description, for the status line.
func hint(b key.Binding, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
}

// keyLine renders bindings as a one-line hint inside a page ("↑/↓ choose •
// esc back"), so a page's keys stay visible even when the status line is
// busy with a message.
func keyLine(bs []key.Binding) string {
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		parts = append(parts, b.Help().Key+" "+b.Help().Desc)
	}
	return faintStyle.Render(strings.Join(parts, " • "))
}

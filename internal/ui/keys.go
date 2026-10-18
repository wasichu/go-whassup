package ui

import "charm.land/bubbles/v2/key"

// keys is the single source of truth for key bindings: handlers match
// against it and the status-line hints and ? overlay are generated from it.
var keys = struct {
	// Chat list (main screen, list focused).
	Up, Down, Open, Quit, Find, Search, Info, Login, Settings, Help key.Binding
	// Message input focused. Attachments use ctrl keys there: plain
	// letters are typed.
	Send, Blur, Media, Attach key.Binding
	// Main screen, either focus.
	PageUp, PageDown key.Binding
	// Lists on the login and settings screens.
	Select, Back, Submit, Cancel, Edit key.Binding
	// Everywhere.
	ForceQuit key.Binding
}{
	Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "previous")),
	Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "next")),
	Open:      key.NewBinding(key.WithKeys("enter", "tab", "right", "l"), key.WithHelp("enter", "write")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Find:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "find/new chat")),
	Search:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "search messages")),
	Media:     key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "attachments")),
	Attach:    key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "send file")),
	Info:      key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "chat info")),
	Login:     key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "link/re-link")),
	Settings:  key.NewBinding(key.WithKeys(","), key.WithHelp(",", "settings")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
	Blur:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "chats")),
	PageUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
	PageDown:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
	Select:    key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "select")),
	Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Edit:      key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "change")),
	Submit:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
	Cancel:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
}

// Binding sets per context, rendered with help.ShortHelpView/FullHelpView.
var (
	listHelp   = []key.Binding{keys.Up, keys.Down, keys.Open, keys.Find, keys.Search, keys.Info, keys.Login, keys.Settings, keys.Help, keys.Quit}
	inputHelp  = []key.Binding{keys.Send, keys.Blur, keys.Media, keys.Attach, keys.PageUp, keys.PageDown}
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
	settingsHelp = []key.Binding{keys.Up, keys.Down, keys.Edit, settingsPrev, keys.Back}
	fullMainHelp = [][]key.Binding{
		{keys.Up, keys.Down, keys.Open, keys.Find, keys.Search, keys.Info, keys.Quit},
		{keys.Send, keys.Blur, keys.Media, keys.Attach, keys.PageUp, keys.PageDown},
		{keys.Login, keys.Settings, keys.Help, keys.ForceQuit},
	}
)

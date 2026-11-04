package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/fuzzy"
)

type uiAction struct {
	title, shortcut string
	run             func(Model) (Model, tea.Cmd)
	score           int
}

type actionsPage struct {
	input        textinput.Model
	all, matches []uiAction
	selected     int
}

func (m Model) openActions() (Model, tea.Cmd) {
	in := newInput()
	in.Prompt, in.Placeholder, in.CharLimit = "Action: ", "type to find an action", 80
	in.SetVirtualCursor(true)
	in.SetWidth(max(m.width-12, 1))
	m.actions = actionsPage{input: in, all: m.availableActions()}
	m.actions.filter()
	m.screen = screenActions
	cmd := m.actions.input.Focus()
	return m, cmd
}

func (m Model) availableActions() []uiAction {
	if m.conv.picking {
		return m.selectedActions()
	}
	var actions []uiAction
	if m.conv.chat.JID != "" {
		if m.deps.Voice != nil && m.conn == connOnline {
			actions = append(actions, uiAction{"Record voice message", "ctrl+g", Model.openVoiceRecorder, 0})
		}
		actions = append(actions, uiAction{"Attach file", "ctrl+t", Model.openAttach, 0})
		if m.draft == "" {
			actions = append(actions, uiAction{"Browse attachments", "ctrl+o", Model.openPicker, 0},
				uiAction{"Select a message", "ctrl+r", selectMessageAction, 0}, uiAction{"Chat info", "i", Model.openInfo, 0})
		}
	}
	return append(actions,
		uiAction{"Find a chat or contact", "/", Model.openFinder, 0},
		uiAction{"Search messages", "s", Model.openSearch, 0},
		uiAction{"Settings", ",", func(m Model) (Model, tea.Cmd) { return m.openSettings(), nil }, 0},
		uiAction{"Link or re-link device", "L", func(m Model) (Model, tea.Cmd) { return m.openLogin(""), nil }, 0},
		uiAction{"Keyboard shortcuts", "F1", func(m Model) (Model, tea.Cmd) { m.showHelp, m.helpScroll = true, 0; return m, nil }, 0})
}

func selectMessageAction(m Model) (Model, tea.Cmd) {
	m.focus = focusInput
	m.conv.startPicking()
	cmd := m.conv.input.Focus()
	return m, cmd
}

func (m Model) selectedActions() []uiAction {
	target, ok := m.conv.pickedMessage()
	if !ok || target.Deleted {
		return nil
	}
	var actions []uiAction
	if target.Media != nil {
		actions = append(actions, uiAction{"Open attachment / play voice", "enter", Model.openPickedAttachment, 0})
	}
	for _, choice := range []struct{ title, shortcut string }{
		{"Reply to message", "r"}, {"React to message", "a"}, {"Sender and reactions", "i"}, {"Copy message text", "c"},
	} {
		actions = append(actions, uiAction{choice.title, choice.shortcut, pickedAction(choice.shortcut), 0})
	}
	if target.FromMe {
		actions = append(actions, uiAction{"Edit message", "e", Model.startEdit, 0}, uiAction{"Delete message for everyone", "d", Model.startDelete, 0})
	}
	return actions
}

func pickedAction(shortcut string) func(Model) (Model, tea.Cmd) {
	return func(m Model) (Model, tea.Cmd) {
		return m.pickKey(tea.KeyPressMsg{Code: []rune(shortcut)[0], Text: shortcut})
	}
}

func (a *actionsPage) filter() {
	a.matches, a.selected = nil, 0
	query := strings.TrimSpace(a.input.Value())
	for _, item := range a.all {
		score, ok := fuzzy.Score(query, item.title)
		if ok {
			item.score = score
			a.matches = append(a.matches, item)
		}
	}
	if query != "" {
		slices.SortStableFunc(a.matches, func(a, b uiAction) int { return b.score - a.score })
	}
}

func (a *actionsPage) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(k, keys.Back):
			return true, nil
		case k.Code == tea.KeyUp:
			a.selected = max(a.selected-1, 0)
			return false, nil
		case k.Code == tea.KeyDown:
			a.selected = min(a.selected+1, max(len(a.matches)-1, 0))
			return false, nil
		case key.Matches(k, keys.Send):
			if len(a.matches) == 0 {
				return false, nil
			}
			item := a.matches[a.selected]
			return false, r.performAction(item)
		}
	}
	before := a.input.Value()
	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	if before != a.input.Value() {
		a.filter()
	}
	return false, cmd
}

func (r rootAPI) performAction(item uiAction) tea.Cmd {
	m := *r.m
	m.screen = screenMain
	next, cmd := item.run(m)
	*r.m = next
	return cmd
}

func (*actionsPage) help() []key.Binding { return finderHelp }

func (a *actionsPage) view(_ rootAPI, width, height int) string {
	lines := []string{paneTitleStyle.Render("Actions"), "", a.input.View(), ""}
	rows := max(height-6, 1)
	start := max(0, a.selected-rows+1)
	if len(a.matches) == 0 {
		lines = append(lines, faintStyle.Render("No matching actions."))
	}
	for i := start; i < len(a.matches) && i < start+rows; i++ {
		item := a.matches[i]
		label := "  " + item.title
		if i == a.selected {
			label = activeSelStyle.Render("› " + item.title)
		}
		line := label + "  " + faintStyle.Render(item.shortcut)
		lines = append(lines, lipgloss.NewStyle().MaxWidth(max(width-4, 1)).Render(line))
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n"))
}

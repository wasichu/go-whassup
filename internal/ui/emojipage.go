package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/emoji"
	"github.com/wasichu/go-whassup/internal/wa"
)

// emojiResults caps the list; refine the query to find others.
const emojiResults = 200

var emojiHelp = []key.Binding{
	key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose")),
	key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "react")),
	keys.Back,
}

// emojiPage is "+" on the reaction strip: search every emoji by name or
// keyword ("laugh" finds 😂) and react with it.
type emojiPage struct {
	target   wa.Message // what's being reacted to, fixed when the page opens
	summary  string     // its text on one line, mentions named
	chat     string
	input    textinput.Model
	results  []emoji.Emoji
	selected int
}

func (m Model) openEmoji(target wa.Message) (Model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = "Search: "
	in.Placeholder = "laugh, heart, party, pizza…"
	in.SetVirtualCursor(true)
	in.CharLimit = 40
	in.SetWidth(max(m.width-12, 10))
	m.screen = screenEmoji
	m.emojiPick = emojiPage{target: target, summary: m.conv.oneLine(target), chat: m.conv.chat.JID, input: in, results: emoji.Search("", emojiResults)}
	cmd := m.emojiPick.input.Focus() // evaluate before m is copied into the return
	return m, cmd
}

func (p *emojiPage) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return false, cmd
	}
	switch k.String() {
	case "esc":
		return true, nil
	case "up", "ctrl+p":
		p.selected = max(p.selected-1, 0)
		return false, nil
	case "down", "ctrl+n":
		p.selected = min(p.selected+1, max(len(p.results)-1, 0))
		return false, nil
	case "enter":
		if p.selected >= len(p.results) {
			return false, nil
		}
		return true, react(r.ctx(), r.deps().Client, p.chat, p.target, p.results[p.selected].Glyph)
	}
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(k)
	if p.input.Value() != before {
		p.results, p.selected = emoji.Search(p.input.Value(), emojiResults), 0
	}
	return false, cmd
}

func (*emojiPage) help() []key.Binding { return emojiHelp }

func (p *emojiPage) view(r rootAPI, width, height int) string {
	who := senderName(p.target, r.chat())
	if p.target.FromMe {
		who = "yourself"
	}
	title := paneTitleStyle.Render("React to "+who+": ") + faintStyle.Render(p.summary)
	lines := []string{lipgloss.NewStyle().MaxWidth(width - 2).Render(title), "", p.input.View(), ""}
	if len(p.results) == 0 {
		lines = append(lines, faintStyle.Render("No emoji match."))
	}
	rows := max(height-len(lines)-3, 1)
	start := min(max(p.selected-rows/2, 0), max(len(p.results)-rows, 0))
	for i := start; i < len(p.results) && i < start+rows; i++ {
		e := p.results[i]
		label := fmt.Sprintf("%s  %s", e.Glyph, e.Name)
		if i == p.selected {
			lines = append(lines, activeSelStyle.Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	return pageStyle(width, height).Render(strings.Join(lines, "\n") + "\n\n" + keyLine(emojiHelp))
}

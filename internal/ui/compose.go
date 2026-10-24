package ui

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Replying and reacting, from the message box: ctrl+r picks a message
// (↑/↓), then r replies to it or e reacts. Reacting offers WhatsApp's
// quick reactions plus "+", which searches every emoji (emojiPage).

// quickReactions are WhatsApp's default quick reactions.
var quickReactions = [...]string{"👍", "❤️", "😂", "😮", "😢", "🙏"}

// moreReactions is the strip's last choice: search every emoji.
const moreReactions = len(quickReactions)

// compose is the message box's reply/react/edit state, kept with the
// conversation it acts on.
type compose struct {
	picking          bool        // choosing a message with ↑/↓
	picked           string      // its ID (an ID, so new messages can't shift it)
	reacting         bool        // choosing a reaction for the picked message
	confirmingDelete bool        // asking before deleting the picked message for everyone
	choice           int         // index into quickReactions, or moreReactions
	replyTo          *wa.Message // the message being replied to, shown above the input
	editing          *wa.Message // your message being edited, its text in the input
	stash            string      // what was typed before the edit, put back after
}

// pickable reports whether a message can be replied or reacted to.
func pickable(m wa.Message) bool { return !m.Deleted }

// startPicking highlights the newest message that can be picked.
func (c *conversation) startPicking() bool {
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if pickable(c.msgs[i]) {
			c.picking, c.picked = true, c.msgs[i].ID
			c.showPicked()
			return true
		}
	}
	return false
}

// movePick moves the highlight to the previous (-1) or next (+1) pickable
// message.
func (c *conversation) movePick(delta int) {
	i := slices.IndexFunc(c.msgs, func(m wa.Message) bool { return m.ID == c.picked })
	for j := i + delta; j >= 0 && j < len(c.msgs); j += delta {
		if pickable(c.msgs[j]) {
			c.picked = c.msgs[j].ID
			c.showPicked()
			return
		}
	}
}

func (c *conversation) showPicked() {
	c.highlight = c.picked
	c.render(false)
	c.showMessage(c.picked)
}

// stopPicking ends picking, reacting and confirming, and drops the
// highlight.
func (c *conversation) stopPicking() {
	c.picking, c.reacting, c.confirmingDelete, c.picked = false, false, false, ""
	c.highlight = ""
	c.render(false)
}

// pickedMessage is the picked message, if it's still loaded.
func (c *conversation) pickedMessage() (wa.Message, bool) {
	i := slices.IndexFunc(c.msgs, func(m wa.Message) bool { return m.ID == c.picked })
	if i < 0 {
		return wa.Message{}, false
	}
	return c.msgs[i], true
}

// setReply starts (or with nil, cancels) a reply; the banner takes a line
// from the message pane.
func (c *conversation) setReply(m *wa.Message) {
	c.replyTo = m
	c.resize(c.width, c.height)
}

// replyBanner is the line above the input while replying.
func (c *conversation) replyBanner(width int) string {
	m := c.replyTo
	who := senderName(*m, c.chat)
	if m.FromMe {
		who = "yourself"
	}
	line := faintStyle.Render("↪ Replying to "+who+": ") + c.oneLine(*m)
	return ansi.Truncate(line+faintStyle.Render("  (esc cancels)"), width, "…")
}

// reactionStrip replaces the input while choosing a reaction: the quick
// reactions and "+", the choice highlighted, your current one marked.
func (c *conversation) reactionStrip(mine string) string {
	var b strings.Builder
	b.WriteString(faintStyle.Render("React: "))
	for i, e := range append(slices.Clone(quickReactions[:]), "+") {
		label := " " + e + " "
		switch {
		case i == c.choice:
			label = activeSelStyle.Render(label)
		case e == mine:
			label = myReactionStyle.Render(label)
		}
		b.WriteString(label)
	}
	return b.String() + faintStyle.Render("  1-6, + any emoji, or ←/→ and enter")
}

// myReaction is the emoji you've reacted to m with, if any.
func myReaction(m wa.Message) string {
	for _, r := range m.Reactions {
		if r.Mine {
			return r.Emoji
		}
	}
	return ""
}

// isPlus reports a "+" however the terminal sends it: as typed text, or
// (with the kitty keyboard protocol) as shift+= with "+" as the shifted key.
func isPlus(msg tea.KeyPressMsg) bool {
	return msg.Text == "+" || msg.Code == '+' || msg.ShiftedCode == '+'
}

// The outcomes of c, scoped: the next key while picking clears them.
const (
	copiedNotice = "Copied the message text."
	noTextNotice = "That message has no text to copy."
)

// pickKey handles keys while picking a message: ↑/↓ move, r replies, e
// reacts (where + searches every emoji), i shows who sent it and who
// reacted, c copies its text, E edits it and d deletes it for everyone
// (edit.go), esc goes back to typing.
// Other keys do nothing (nothing types).
func (m Model) pickKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	c := &m.conv
	m.clearScoped() // a "Copied" was about the previous key
	switch {
	case key.Matches(msg, keys.Blur):
		c.stopPicking()
	case key.Matches(msg, keys.Up):
		c.movePick(-1)
	case key.Matches(msg, keys.Down):
		c.movePick(1)
	case msg.String() == "r":
		if target, ok := c.pickedMessage(); ok {
			c.stopPicking()
			c.setEditing(nil)
			c.setReply(&target)
		}
	case msg.String() == "e":
		if target, ok := c.pickedMessage(); ok {
			c.reacting, c.choice = true, max(slices.Index(quickReactions[:], myReaction(target)), 0)
		}
	case msg.String() == "i":
		if target, ok := c.pickedMessage(); ok {
			return m.openSender(target)
		}
	case msg.String() == "c":
		return m.copyPicked()
	case msg.String() == "d":
		return m.startDelete()
	case isEditKey(msg):
		return m.startEdit()
	}
	return m, nil
}

// copyPicked copies the selected message's text (or caption) to the
// clipboard, with mentions named as shown ("@Ada").
func (m Model) copyPicked() (Model, tea.Cmd) {
	target, ok := m.conv.pickedMessage()
	if !ok {
		return m, nil
	}
	text := plainMentions(target.Text, m.conv.mentions, m.conv.own)
	if strings.TrimSpace(text) == "" {
		m.sayScoped(noTextNotice)
		return m, nil
	}
	m.sayScoped(copiedNotice)
	return m, tea.SetClipboard(text)
}

// reactKey handles keys on the reaction strip.
func (m Model) reactKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	c := &m.conv
	switch k := msg.String(); {
	case key.Matches(msg, keys.Blur):
		c.reacting = false // back to picking
	case k == "left" || k == "h":
		c.choice = max(c.choice-1, 0)
	case k == "right" || k == "l":
		c.choice = min(c.choice+1, moreReactions)
	case len(k) == 1 && k >= "1" && k <= "6":
		c.choice = int(k[0] - '1')
		return m.chooseReaction()
	case isPlus(msg):
		c.choice = moreReactions
		return m.chooseReaction()
	case k == "enter":
		return m.chooseReaction()
	}
	return m, nil
}

// chooseReaction sends the strip's choice, or opens emoji search for "+".
func (m Model) chooseReaction() (Model, tea.Cmd) {
	target, ok := m.conv.pickedMessage()
	m.conv.stopPicking()
	if !ok {
		return m, nil
	}
	if m.conv.choice == moreReactions {
		return m.openEmoji(target)
	}
	return m, react(m.ctx, m.deps.Client, m.conv.chat.JID, target, quickReactions[m.conv.choice])
}

// react sends emoji as your reaction to target; choosing the reaction you
// already have removes it, as on the phone.
func react(ctx context.Context, client wa.Client, chat string, target wa.Message, emoji string) tea.Cmd {
	if myReaction(target) == emoji {
		emoji = ""
	}
	return func() tea.Msg {
		return sendResultMsg{err: client.SendReaction(ctx, chat, target, emoji)}
	}
}

// sendReply sends text as a reply to the message being replied to.
func sendReply(ctx context.Context, client wa.Client, chat, text string, to wa.Message) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: client.SendReply(ctx, chat, text, to)}
	}
}

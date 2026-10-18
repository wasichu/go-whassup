package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// conversation is the right pane: header, message history and input.
type conversation struct {
	chat     store.ChatSummary
	msgs     []wa.Message
	viewport viewport.Model
	input    textinput.Model
	width    int
}

func newConversation() conversation {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "Type a message, Enter to send"
	in.SetVirtualCursor(true) // the view is composed of panes; a drawn cursor is simplest
	in.CharLimit = 4096
	return conversation{viewport: viewport.New(), input: in}
}

// resize lays out the pane in width x height (header + history + input).
func (c *conversation) resize(width, height int) {
	c.width = width
	c.viewport.SetWidth(width)
	c.viewport.SetHeight(max(height-3, 1)) // header, separator, input
	c.input.SetWidth(max(width-lipgloss.Width(c.input.Prompt)-1, 1))
	c.render(true)
}

// setChat switches to a chat, clearing what was shown. Re-setting the same
// chat (e.g. after a chat list reload) only refreshes its metadata and keeps
// the scroll position unless the user was already at the bottom.
func (c *conversation) setChat(chat store.ChatSummary) {
	switched := chat.JID != c.chat.JID
	if switched {
		c.msgs = nil
	}
	c.chat = chat
	c.render(switched || c.viewport.AtBottom())
}

// setMessages shows msgs. The view follows new messages only if the user
// was already at the bottom (or the chat just opened).
func (c *conversation) setMessages(msgs []wa.Message) {
	follow := c.viewport.AtBottom() || len(c.msgs) == 0
	c.msgs = msgs
	c.render(follow)
}

func (c *conversation) render(follow bool) {
	content := renderMessages(c.msgs, c.chat, c.width)
	// Like chat apps, anchor short conversations to the bottom, by the input.
	if gap := c.viewport.Height() - lipgloss.Height(content); gap > 0 {
		content = strings.Repeat("\n", gap) + content
	}
	c.viewport.SetContent(content)
	if follow {
		c.viewport.GotoBottom()
	}
}

func (c *conversation) view(width, height int, own string) string {
	if c.chat.JID == "" {
		return lipgloss.NewStyle().Width(width).Height(height).Render(faintStyle.Render("Select a chat"))
	}
	header := paneTitleStyle.Render(chatTitle(c.chat, own))
	if c.chat.Name != "" {
		header += "  " + faintStyle.Render(c.chat.JID)
	}
	parts := []string{
		lipgloss.NewStyle().MaxWidth(width).Render(header),
		c.viewport.View(),
		separatorStyle.Render(strings.Repeat("─", width)),
		c.input.View(),
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(parts, "\n"))
}

// renderMessages formats msgs as wrapped lines with day separators.
func renderMessages(msgs []wa.Message, chat store.ChatSummary, width int) string {
	if width <= 0 {
		return ""
	}
	if len(msgs) == 0 {
		return faintStyle.Render("No messages here yet. Say hi!")
	}
	wrap := lipgloss.NewStyle().Width(width)
	var b strings.Builder
	var lastDay string
	for i, m := range msgs {
		local := m.Timestamp.Local()
		if day := local.Format("Mon, Jan 2 2006"); day != lastDay {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(daySepStyle.Width(width).Render("── "+day+" ──") + "\n")
			lastDay = day
		}
		name := senderStyle.Render(senderName(m, chat))
		if m.FromMe {
			name = fromMeStyle.Render("me")
		}
		line := faintStyle.Render(local.Format("15:04")) + " " + name + ": " + m.Summary()
		b.WriteString(wrap.Render(line) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// senderName labels an incoming message: in a DM the chat's own title, in a
// group the sender's best known name (address book, push name), falling
// back to their number.
func senderName(m wa.Message, chat store.ChatSummary) string {
	if !m.IsGroup && chat.Name != "" {
		return chat.Name
	}
	if m.SenderName != "" {
		return m.SenderName
	}
	if m.PushName != "" {
		return m.PushName
	}
	user, _, _ := strings.Cut(m.Sender, "@")
	return user
}

package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// conversation is the right pane: header, message history and input.
type conversation struct {
	chat     store.ChatSummary
	cached   map[string]bool   // attachments already on disk, by message ID
	mentions map[string]string // phone digits -> display name, for @mentions
	own      string            // the user's own JID, to highlight mentions of them
	newFrom  time.Time         // messages after this get a "new" divider (set on opening)
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
		c.newFrom = time.Time{}
		if chat.Unread > 0 {
			c.newFrom = chat.LastReadAt // keep the divider while the chat stays open
		}
	}
	c.chat = chat
	c.render(switched || c.viewport.AtBottom())
}

// setMessages shows msgs. The view follows new messages only if the user
// was already at the bottom (or the chat just opened).
func (c *conversation) setMessages(msgs []wa.Message) {
	first := len(c.msgs) == 0
	follow := c.viewport.AtBottom() || first
	c.msgs = msgs
	c.render(follow)
	// Opening a chat with unread messages: start at the "new" divider, as
	// the phone does, unless everything new already fits on screen.
	if first && len(msgs) > 0 && !c.newFrom.IsZero() {
		if line := c.dividerLine(); line >= 0 && c.viewport.TotalLineCount()-line > c.viewport.Height() {
			c.viewport.SetYOffset(max(line-1, 0))
		}
	}
}

func (c *conversation) render(follow bool) {
	content := renderMessages(c.msgs, c.chat, c.width, renderOpts{newFrom: c.newFrom, cached: c.cached, mentions: c.mentions, own: c.own})
	// Like chat apps, anchor short conversations to the bottom, by the input.
	if gap := c.viewport.Height() - lipgloss.Height(content); gap > 0 {
		content = strings.Repeat("\n", gap) + content
	}
	c.viewport.SetContent(content)
	if follow {
		c.viewport.GotoBottom()
	}
}

// dividerLine is the viewport line of the "new" divider, or -1.
func (c *conversation) dividerLine() int {
	for i, line := range strings.Split(c.viewport.GetContent(), "\n") {
		if strings.Contains(line, " new ──") {
			return i
		}
	}
	return -1
}

func (c *conversation) view(width, height int, own string) string {
	if c.chat.JID == "" {
		return lipgloss.NewStyle().Width(width).Height(height).Render(faintStyle.Render("Select a chat"))
	}
	header := paneTitleStyle.Render(chatTitle(c.chat, own))
	switch {
	case c.chat.IsGroup && c.chat.MemberCount > 0:
		header += "  " + faintStyle.Render(fmt.Sprintf("%d members · i for info", c.chat.MemberCount))
	case c.chat.IsGroup:
		header += "  " + faintStyle.Render("group · i for info")
	case c.chat.Name != "":
		user, _, _ := strings.Cut(c.chat.JID, "@")
		header += "  " + faintStyle.Render("+"+user)
	}
	parts := []string{
		lipgloss.NewStyle().MaxWidth(width).Render(header),
		c.viewport.View(),
		separatorStyle.Render(strings.Repeat("─", width)),
		c.input.View(),
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(parts, "\n"))
}

// renderOpts carries what renderMessages needs besides the messages.
type renderOpts struct {
	newFrom  time.Time         // "N new" divider before the first incoming message after this
	cached   map[string]bool   // message IDs whose attachment is on disk
	mentions map[string]string // phone digits -> display name
	own      string            // user's JID: mentions of them are highlighted
}

// renderMessages formats msgs as wrapped lines with day separators, and a
// "N new" divider before the first incoming message after opts.newFrom.
func renderMessages(msgs []wa.Message, chat store.ChatSummary, width int, opts renderOpts) string {
	newFrom, cached := opts.newFrom, opts.cached
	if width <= 0 {
		return ""
	}
	if len(msgs) == 0 {
		return faintStyle.Render("No messages here yet. Say hi!")
	}
	wrap := lipgloss.NewStyle().Width(width)
	var b strings.Builder
	var lastDay string
	newCount, dividerAt := 0, -1
	if !newFrom.IsZero() {
		for i, m := range msgs {
			if !m.FromMe && m.Timestamp.After(newFrom) {
				if dividerAt < 0 {
					dividerAt = i
				}
				newCount++
			}
		}
	}
	for i, m := range msgs {
		if i == dividerAt {
			b.WriteString(newDividerStyle.Width(width).Render(fmt.Sprintf("── %d new ──", newCount)) + "\n")
		}
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
		body := renderMentions(m.Summary(), opts.mentions, opts.own)
		switch {
		case m.Deleted:
			body = deletedStyle.Render("This message was deleted")
		case m.Edited:
			body += " " + faintStyle.Render("(edited)")
		}
		if m.Media != nil && !m.Deleted {
			info := humanSize(m.Media.Size)
			if cached[m.ID] {
				info += " ✓"
			}
			if info != "" {
				body += " " + faintStyle.Render("· "+info)
			}
		}
		line := faintStyle.Render(local.Format("15:04")) + " " + name + ": " + body
		b.WriteString(wrap.Render(line) + "\n")
		if r := renderReactions(m.Reactions); r != "" {
			b.WriteString(wrap.Render("      "+r) + "\n")
		}
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

// renderReactions summarises reactions as "👍 2  ❤️", marking the user's own.
func renderReactions(rs []wa.ReactionCount) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		p := r.Emoji
		if r.Count > 1 {
			p += " " + fmt.Sprint(r.Count)
		}
		if r.Mine {
			p = myReactionStyle.Render(p)
		} else {
			p = reactionStyle.Render(p)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "  ")
}

// humanSize renders a byte count like "2.3 MB"; "" for unknown.
func humanSize(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

var mentionRE = regexp.MustCompile(`@(\d{5,15})\b`)

// renderMentions shows "@15551234567" as "@Ada" (styled), highlighting
// mentions of the user. Unknown numbers stay as they are.
func renderMentions(text string, names map[string]string, own string) string {
	ownUser, _, _ := strings.Cut(own, "@")
	return mentionRE.ReplaceAllStringFunc(text, func(tok string) string {
		digits := tok[1:]
		if digits == ownUser && ownUser != "" {
			return myMentionStyle.Render("@you")
		}
		if name, ok := names[digits]; ok {
			return mentionStyle.Render("@" + name)
		}
		return tok
	})
}

// mentionedDigits lists the phone numbers mentioned in msgs.
func mentionedDigits(msgs []wa.Message) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range msgs {
		for _, match := range mentionRE.FindAllStringSubmatch(m.Text, -1) {
			if !seen[match[1]] {
				seen[match[1]] = true
				out = append(out, match[1])
			}
		}
	}
	return out
}

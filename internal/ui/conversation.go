package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// conversation is the right pane: header, message history and input.
type conversation struct {
	chat      store.ChatSummary
	cached    map[string]bool       // attachments already on disk, by message ID
	mentions  map[string]string     // phone digits -> display name, for @mentions
	quotes    map[string]wa.Message // what replies answer, by message ID
	own       string                // the user's own JID, to highlight mentions of them
	highlight string                // message to highlight and show (a search hit)
	lineOf    map[string]int        // each rendered message's first line in the viewport
	newFrom   time.Time             // messages after this get a "new" divider (set on opening)
	msgs      []wa.Message
	viewport  viewport.Model
	input     textinput.Model
	width     int
	height    int
	compose   // replying and reacting (compose.go)
}

func newConversation() conversation {
	in := newInput()
	in.Prompt = "> "
	in.Placeholder = "Type a message, Enter to send"
	in.SetVirtualCursor(true) // the view is composed of panes; a drawn cursor is simplest
	in.CharLimit = 4096
	return conversation{viewport: viewport.New(), input: in}
}

// resize lays out the pane in width x height (header + history + input).
func (c *conversation) resize(width, height int) {
	c.width, c.height = width, height
	c.viewport.SetWidth(width)
	chrome := 3 // header, separator, input
	if c.replyTo != nil {
		chrome++ // the reply banner
	}
	c.viewport.SetHeight(max(height-chrome, 1))
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
		c.highlight = ""
		c.compose = compose{} // a reply or pick belongs to the chat it started in
		defer c.resize(c.width, c.height)
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
	c.render(follow && c.highlight == "")
	if c.highlight != "" {
		c.showMessage(c.highlight)
		return
	}
	// Opening a chat with unread messages: start at the "new" divider, as
	// the phone does, unless everything new already fits on screen.
	if first && len(msgs) > 0 && !c.newFrom.IsZero() {
		if line := c.dividerLine(); line >= 0 && c.viewport.TotalLineCount()-line > c.viewport.Height() {
			c.viewport.SetYOffset(max(line-1, 0))
		}
	}
}

func (c *conversation) render(follow bool) {
	c.lineOf = map[string]int{}
	content := renderMessages(c.msgs, c.chat, c.width, renderOpts{
		newFrom: c.newFrom, cached: c.cached, mentions: c.mentions, quotes: c.quotes, own: c.own,
		highlight: c.highlight, lineOf: c.lineOf,
	})
	// Like chat apps, anchor short conversations to the bottom, by the input.
	gap := max(c.viewport.Height()-lipgloss.Height(content), 0)
	if gap > 0 {
		content = strings.Repeat("\n", gap) + content
	}
	for id := range c.lineOf {
		c.lineOf[id] += gap
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
	}
	if c.replyTo != nil {
		parts = append(parts, c.replyBanner(width))
	}
	if picked, ok := c.pickedMessage(); c.reacting && ok {
		parts = append(parts, c.reactionStrip(myReaction(picked))) // in place of the input
	} else {
		parts = append(parts, c.input.View())
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(parts, "\n"))
}

// renderOpts carries what renderMessages needs besides the messages.
type renderOpts struct {
	newFrom   time.Time             // "N new" divider before the first incoming message after this
	cached    map[string]bool       // message IDs whose attachment is on disk
	mentions  map[string]string     // phone digits -> display name
	quotes    map[string]wa.Message // what replies answer, by message ID
	own       string                // user's JID: mentions of them are highlighted
	highlight string                // message ID to highlight (a search hit)
	lineOf    map[string]int        // if set, filled with each message's first line
}

// renderMessages formats msgs as wrapped lines with day separators, and a
// "N new" divider before the first incoming message after opts.newFrom.
func renderMessages(msgs []wa.Message, chat store.ChatSummary, width int, opts renderOpts) string {
	if width <= 0 {
		return ""
	}
	if len(msgs) == 0 {
		return faintStyle.Render("No messages here yet. Say hi!")
	}
	wrap := lipgloss.NewStyle().Width(width)
	var b strings.Builder
	var lastDay string
	newCount, dividerAt := newDivider(msgs, opts.newFrom)
	for i, m := range msgs {
		if i == dividerAt {
			b.WriteString(newDividerStyle.Width(width).Render(fmt.Sprintf("── %d new ──", newCount)) + "\n")
		}
		if day := m.Timestamp.Local().Format("Mon, Jan 2 2006"); day != lastDay {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(daySepStyle.Width(width).Render("── "+day+" ──") + "\n")
			lastDay = day
		}
		line := messageLine(m, chat, opts)
		if opts.lineOf != nil {
			opts.lineOf[m.ID] = strings.Count(b.String(), "\n")
		}
		if q := quoteLine(m, chat, opts); q != "" {
			b.WriteString(ansi.Truncate(q, width, "…") + "\n") // one line, cut to fit
		}
		if m.ID == opts.highlight {
			b.WriteString(highlightStyle.Width(width).Render(line) + "\n")
		} else {
			b.WriteString(wrap.Render(line) + "\n")
		}
		if r := renderReactions(m.Reactions); r != "" {
			b.WriteString(wrap.Render("      "+r) + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// newDivider counts the messages from others after newFrom and returns
// where the "N new" divider goes (-1 for none).
func newDivider(msgs []wa.Message, newFrom time.Time) (count, at int) {
	at = -1
	if newFrom.IsZero() {
		return 0, at
	}
	for i, m := range msgs {
		if !m.FromMe && m.Timestamp.After(newFrom) {
			if at < 0 {
				at = i
			}
			count++
		}
	}
	return count, at
}

// messageLine is one message's unwrapped line: time, sender and body
// (with edit/delete markers and attachment info).
func messageLine(m wa.Message, chat store.ChatSummary, opts renderOpts) string {
	who := senderName(m, chat)
	name := nameStyle(who, senderStyle).Render(who)
	if m.FromMe {
		name = fromMeStyle.Render("me")
	}
	body := renderLinks(renderMentions(m.Summary(), opts.mentions, opts.own))
	switch {
	case m.Deleted:
		return faintStyle.Render(m.Timestamp.Local().Format("15:04")) + " " + name + ": " + deletedStyle.Render("This message was deleted")
	case m.Edited:
		body += " " + faintStyle.Render("(edited)")
	}
	if m.Media != nil {
		info := humanSize(m.Media.Size)
		if opts.cached[m.ID] {
			info += " ✓"
		}
		if info != "" {
			body += " " + faintStyle.Render("· "+info)
		}
	}
	return faintStyle.Render(m.Timestamp.Local().Format("15:04")) + " " + name + ": " + body
}

// senderName labels an incoming message: in a DM the chat's own title, in a
// group the sender's best known name (address book, else "~" and the name
// they chose), falling back to their number, or "unknown member" for a
// privacy ID.
func senderName(m wa.Message, chat store.ChatSummary) string {
	if !m.IsGroup && chat.Name != "" {
		return chat.Name
	}
	if m.SenderName != "" {
		return m.SenderName
	}
	if m.PushName != "" {
		return "~" + m.PushName
	}
	return numberOf(m.Sender)
}

// numberOf is how a person without a name is shown: "+15551234567", or
// "unknown member" for a privacy ID (which isn't their number).
func numberOf(jid string) string {
	if store.IsPrivacyID(jid) || jid == "" {
		return store.UnknownPerson
	}
	user, _, _ := strings.Cut(jid, "@")
	return "+" + user
}

// nameStyle dims a name that's only a number or "unknown member", so real
// names stand out.
func nameStyle(name string, named lipgloss.Style) lipgloss.Style {
	if strings.HasPrefix(name, "+") || name == store.UnknownPerson {
		return faintStyle
	}
	return named
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
		return faintStyle.Render(tok) // a number: dimmed, so names stand out
	})
}

// plainMentions is renderMentions without styling, for text shown in one
// style (quotes, the reply banner): "@Ada", "@you".
func plainMentions(text string, names map[string]string, own string) string {
	return ansi.Strip(renderMentions(text, names, own))
}

// oneLine is a message's summary on one line, mentions named.
func (c *conversation) oneLine(m wa.Message) string {
	return strings.Join(strings.Fields(plainMentions(m.Summary(), c.mentions, c.own)), " ")
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

// showMessage scrolls so message id is visible near the top, reporting
// whether it is among the loaded messages.
func (c *conversation) showMessage(id string) bool {
	line, ok := c.lineOf[id]
	if ok {
		c.viewport.SetYOffset(max(line-2, 0))
	}
	return ok
}

// quoteLine is the dim "↪ Ada: Tea on Saturday?" line above a reply, or
// "" for messages that aren't replies.
func quoteLine(m wa.Message, chat store.ChatSummary, opts renderOpts) string {
	if m.ReplyTo == nil {
		return ""
	}
	q, ok := opts.quotes[m.ReplyTo.ID]
	if !ok {
		return faintStyle.Render("      ↪ an earlier message")
	}
	who := senderName(q, chat)
	if q.FromMe {
		who = "me"
	}
	text := deletedStyle.Render("This message was deleted")
	if !q.Deleted {
		text = faintStyle.Render(strings.Join(strings.Fields(plainMentions(q.Summary(), opts.mentions, opts.own)), " ")) // on one line
	}
	return faintStyle.Render("      ↪ "+who+": ") + text
}

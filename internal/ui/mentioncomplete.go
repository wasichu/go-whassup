package ui

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/fuzzy"
	"github.com/wasichu/go-whassup/internal/store"
)

// Draft spans use rune offsets, like textinput's cursor. Editing a name removes
// its association rather than silently mentioning someone whose label changed.
type draftMention struct {
	start, end int
	jid        string
}
type mentionChoice struct {
	member store.Member
	label  string
	score  int
}
type mentionCompletion struct {
	members              []store.Member
	matches              []mentionChoice
	selected, start, end int
	dismissed            bool
}

func (a mentionCompletion) rows() int {
	if len(a.matches) == 0 {
		return 0
	}
	return min(len(a.matches), 4) + 1
}

// mentionQuery recognizes a standalone @word around the cursor, excluding
// email addresses and the interior of an already selected name.
func (c *conversation) mentionQuery() (query string, start, end int, ok bool) {
	if !c.chat.IsGroup || c.picking || c.completion.dismissed {
		return
	}
	text := []rune(c.input.Value())
	pos := min(c.input.Position(), len(text))
	start = pos
	for start > 0 && !unicode.IsSpace(text[start-1]) && text[start-1] != '@' {
		start--
	}
	if start == 0 || text[start-1] != '@' {
		return
	}
	start--
	if start > 0 && !mentionBoundary(text[start-1]) {
		return
	}
	if slices.ContainsFunc(c.draftMentions, func(m draftMention) bool { return pos > m.start && pos <= m.end }) {
		return
	}
	end = pos
	for end < len(text) && !unicode.IsSpace(text[end]) {
		end++
	}
	return string(text[start+1 : pos]), start, end, true
}

func mentionBoundary(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("([{", r) }

func (c *conversation) refreshCompletion() {
	a := &c.completion
	oldRows := a.rows()
	a.matches = nil
	query, start, end, ok := c.mentionQuery()
	if ok {
		a.start, a.end = start, end
		a.matches = matchingMembers(a.members, query)
	}
	a.selected = min(a.selected, max(len(a.matches)-1, 0))
	if oldRows != a.rows() {
		c.resize(c.width, c.height)
	}
}

func matchingMembers(members []store.Member, query string) []mentionChoice {
	var matches []mentionChoice
	for _, member := range members {
		label, _ := store.PersonLabel(member.Name, "", member.JID)
		label = strings.Join(strings.Fields(label), " ")
		score, ok := fuzzy.Score(query, label+" "+store.NumberOf(member.JID))
		if ok {
			matches = append(matches, mentionChoice{member, label, score})
		}
	}
	if query != "" {
		slices.SortStableFunc(matches, func(x, y mentionChoice) int { return y.score - x.score })
	}
	return matches
}

func (c *conversation) completeKey(k tea.KeyPressMsg) bool {
	a := &c.completion
	if len(a.matches) == 0 {
		return false
	}
	switch k.Code {
	case tea.KeyUp:
		a.selected = max(a.selected-1, 0)
	case tea.KeyDown:
		a.selected = min(a.selected+1, len(a.matches)-1)
	case tea.KeyEscape:
		a.dismissed = true
		c.refreshCompletion()
	case tea.KeyTab, tea.KeyEnter:
		chosen := a.matches[a.selected]
		before := c.input.Value()
		text := []rune(before)
		token := []rune("@" + chosen.label)
		replacement := append(slices.Clone(text[:a.start]), token...)
		if a.end == len(text) || !unicode.IsSpace(text[a.end]) {
			replacement = append(replacement, ' ')
		}
		replacement = append(replacement, text[a.end:]...)
		if c.input.CharLimit > 0 && len(replacement) > c.input.CharLimit {
			return true
		}
		start := a.start
		c.input.SetValue(string(replacement))
		c.input.SetCursor(start + len(token) + 1)
		c.trackMentions(before)
		c.draftMentions = append(c.draftMentions, draftMention{start, start + len(token), chosen.member.JID})
		slices.SortFunc(c.draftMentions, func(a, b draftMention) int { return a.start - b.start })
		c.refreshCompletion()
	default:
		return false
	}
	return true
}

func (a mentionCompletion) view(width int) []string {
	if len(a.matches) == 0 {
		return nil
	}
	start := max(a.selected-3, 0)
	var lines []string
	for i := start; i < min(start+4, len(a.matches)); i++ {
		choice := a.matches[i]
		label := "  " + choice.label + "  " + store.NumberOf(choice.member.JID)
		if i == a.selected {
			label = activeSelStyle.Render("› "+choice.label) + "  " + faintStyle.Render(store.NumberOf(choice.member.JID))
		}
		lines = append(lines, ansi.Truncate(label, max(width, 1), "…"))
	}
	return append(lines, faintStyle.Render(ansi.Truncate("↑/↓ choose · Tab/Enter mention · Esc close", max(width, 1), "…")))
}

func (c *conversation) trackMentions(before string, cursor ...int) {
	old, now := []rune(before), []rune(c.input.Value())
	if before == string(now) {
		return
	}
	prefix, oldEnd, newEnd := changedMentionRange(old, now, c.input.Position(), cursor)
	var kept []draftMention
	for _, m := range c.draftMentions {
		switch {
		case m.end <= prefix:
			kept = append(kept, m)
		case m.start >= oldEnd:
			m.start += newEnd - oldEnd
			m.end += newEnd - oldEnd
			kept = append(kept, m)
		}
	}
	c.draftMentions = kept
}

var mentionIDRE = regexp.MustCompile(`@([0-9]+)\b`)

// Convert only intact selected names to canonical @IDs. Membership is checked
// at send time; a stale selection cannot mention someone outside this group.
func (c *conversation) encodeMentions() (string, []string) {
	text := []rune(c.input.Value())
	members := map[string]bool{}
	if c.chat.IsGroup {
		for _, m := range c.completion.members {
			members[m.JID] = true
		}
	}
	var out strings.Builder
	var ids []string
	seen := map[string]bool{}
	add := func(jid string) {
		if !seen[jid] {
			ids = append(ids, jid)
			seen[jid] = true
		}
	}
	cursor := 0
	for _, m := range c.draftMentions {
		if m.start < cursor || m.end > len(text) || !members[m.jid] {
			continue
		}
		out.WriteString(string(text[cursor:m.start]))
		user, _, _ := strings.Cut(m.jid, "@")
		out.WriteString("@" + user)
		cursor = m.end
		add(m.jid)
	}
	out.WriteString(string(text[cursor:]))
	result := out.String()
	// Existing messages are stored with canonical numbers. Preserve their mentions
	// when editing, and allow explicitly typed @numbers for known group members.
	tokens := map[string]bool{}
	for _, match := range mentionIDRE.FindAllStringSubmatchIndex(result, -1) {
		if match[0] > 0 {
			prev, _ := utf8.DecodeLastRuneInString(result[:match[0]])
			if !mentionBoundary(prev) {
				continue
			}
		}
		tokens[result[match[2]:match[3]]] = true
	}
	for _, member := range c.completion.members {
		user, _, _ := strings.Cut(member.JID, "@")
		if members[member.JID] && tokens[user] {
			add(member.JID)
		}
	}
	return result, ids
}

// updateInput handles key and paste edits through the same span tracking path.
func (c *conversation) updateInput(msg tea.Msg) tea.Cmd {
	before, pos := c.input.Value(), c.input.Position()
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	c.trackMentions(before, pos)
	if before != c.input.Value() || pos != c.input.Position() {
		c.completion.dismissed = false
	}
	if before != c.input.Value() {
		c.completion.selected = 0
	}
	c.refreshCompletion()
	return cmd
}

// The cursor disambiguates edits to repeated labels (two people named Alex).
// Fall back conservatively for programmatic replacements without a cursor hint.
func changedMentionRange(old, now []rune, newCursor int, oldCursor []int) (prefix, oldEnd, newEnd int) {
	if len(oldCursor) > 0 {
		prefix = min(oldCursor[0], newCursor)
		delta := len(now) - len(old)
		oldEnd = prefix + max(-delta, 0)
		newEnd = prefix + max(delta, 0)
		if prefix >= 0 && oldEnd <= len(old) && newEnd <= len(now) && slices.Equal(old[:prefix], now[:prefix]) && slices.Equal(old[oldEnd:], now[newEnd:]) {
			return
		}
	}
	prefix = 0
	for prefix < min(len(old), len(now)) && old[prefix] == now[prefix] {
		prefix++
	}
	oldEnd, newEnd = len(old), len(now)
	for oldEnd > prefix && newEnd > prefix && old[oldEnd-1] == now[newEnd-1] {
		oldEnd--
		newEnd--
	}
	return
}

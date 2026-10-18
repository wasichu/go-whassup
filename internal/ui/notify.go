package ui

import (
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/wa"
)

const (
	// notifyCooldown spaces notifications for one chat, so a burst of
	// messages raises one.
	notifyCooldown = time.Minute
	// notifyMaxAge skips older messages: they're backlog delivered on
	// reconnect, and the chat list shows them.
	notifyMaxAge = 2 * time.Minute
)

// notify raises a desktop notification for an incoming message when the
// terminal isn't focused (see docs/usage.md), or returns nil. Notifications
// go through the terminal, which shows them natively.
func (m *Model) notify(msg wa.Message, now time.Time) tea.Cmd {
	cfg := m.deps.Config.Notifications
	if !cfg.Enabled || m.deps.Notify.Protocol == termcaps.NotifyNone || msg.FromMe || m.termFocused {
		return nil
	}
	if now.Sub(msg.Timestamp) > notifyMaxAge {
		return nil
	}
	chat, ok := m.chats.find(msg.Chat)
	if !ok {
		chat = store.ChatSummary{JID: msg.Chat, IsGroup: msg.IsGroup}
	}
	if chat.Muted(now) {
		return nil
	}
	if last, ok := m.notified[msg.Chat]; ok && now.Sub(last) < notifyCooldown {
		return nil
	}
	m.notified[msg.Chat] = now
	title, body := notificationText(cfg.Preview, msg, chat, m.ownJID)
	return tea.Raw(notifySequence(m.deps.Notify, title, body))
}

// notificationText is what a notification shows, per the preview setting.
func notificationText(preview string, msg wa.Message, chat store.ChatSummary, own string) (title, body string) {
	switch preview {
	case config.PreviewNone:
		return "go-whassup", "New message"
	case config.PreviewSender:
		if msg.IsGroup {
			return chatTitle(chat, own), senderName(msg, chat) + " sent a message"
		}
		return chatTitle(chat, own), "New message"
	}
	if msg.IsGroup {
		return chatTitle(chat, own), senderName(msg, chat) + ": " + msg.Summary()
	}
	return chatTitle(chat, own), msg.Summary()
}

// notifySequence builds the terminal's notification escape sequence. Title
// and body come from senders, so they're cleaned of anything that could
// end the sequence early or start another.
func notifySequence(n termcaps.Notify, title, body string) string {
	title, body = cleanNotifyText(title, 60), cleanNotifyText(body, 200)
	var seq string
	switch n.Protocol {
	case termcaps.NotifyOSC777:
		// Fields are ';'-separated; the body is the rest, so only the title
		// must not contain one.
		seq = "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + body + "\x1b\\"
	case termcaps.NotifyOSC99:
		seq = "\x1b]99;i=wa:d=0:p=title;" + title + "\x1b\\" + "\x1b]99;i=wa:d=1:p=body;" + body + "\x1b\\"
	case termcaps.NotifyOSC9:
		// Some terminals read "9;<number>;…" as other commands (ConEmu's
		// progress bar), so no ';' at all.
		seq = "\x1b]9;" + strings.ReplaceAll(title+": "+body, ";", ",") + "\x1b\\"
	default:
		return ""
	}
	return passthrough(seq, n.Tmux)
}

// cleanNotifyText drops control characters (C0, DEL, C1: anything that could
// end or inject an escape sequence), folds whitespace, and truncates to max
// runes.
func cleanNotifyText(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxRunes {
		s = string([]rune(s)[:maxRunes-1]) + "…"
	}
	return s
}

package ui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Changing your own messages, from select mode: d deletes the picked
// message for everyone, after a confirmation. WhatsApp allows it only for
// your own messages and only for a while after sending (wa.DeleteWindow);
// otherwise the key says why instead.

var deleteHelp = []key.Binding{
	key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete for everyone")),
	key.NewBinding(key.WithKeys("n"), key.WithHelp("n/esc", "keep it")),
}

// deleteRefusal says why m can't be deleted for everyone at now, or "" if
// it can.
func deleteRefusal(m wa.Message, now time.Time) string {
	switch {
	case !m.FromMe:
		return "You can only delete your own messages."
	case now.Sub(m.Timestamp) > wa.DeleteWindow:
		return "WhatsApp only deletes messages for everyone within 2 days of sending."
	}
	return ""
}

// startDelete asks to confirm deleting the picked message, or says why it
// can't be deleted.
func (m Model) startDelete() (Model, tea.Cmd) {
	target, ok := m.conv.pickedMessage()
	if !ok {
		return m, nil
	}
	if why := deleteRefusal(target, time.Now()); why != "" {
		m.sayScoped(why)
		return m, nil
	}
	m.conv.confirmingDelete = true
	return m, nil
}

// deleteKey handles the confirmation: y or enter deletes, n or esc goes
// back to picking. The window is checked again: the question may have sat
// there a while.
func (m Model) deleteKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	c := &m.conv
	switch k := msg.String(); {
	case k == "y" || k == "enter":
		target, ok := c.pickedMessage()
		c.stopPicking()
		if !ok {
			return m, nil
		}
		if why := deleteRefusal(target, time.Now()); why != "" {
			m.say(why)
			return m, nil
		}
		return m, deleteMessage(m.ctx, m.deps.Client, c.chat.JID, target)
	case k == "n" || key.Matches(msg, keys.Blur):
		c.confirmingDelete = false
	}
	return m, nil
}

// deleteStrip replaces the input while confirming a delete.
func deleteStrip() string {
	return errorStyle.Render("Delete this message for everyone?") + faintStyle.Render("  y delete · n keep")
}

func deleteMessage(ctx context.Context, client wa.Client, chat string, target wa.Message) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: client.SendDelete(ctx, chat, target)}
	}
}

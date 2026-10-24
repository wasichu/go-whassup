package ui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Changing your own messages, from select mode: E puts the picked text
// message in the box to edit (enter sends the edit), and d deletes it for
// everyone after a confirmation. WhatsApp allows each only for your own
// messages and only for a while after sending (wa.EditWindow,
// wa.DeleteWindow); otherwise the key says why instead.

var deleteHelp = []key.Binding{
	key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete for everyone")),
	key.NewBinding(key.WithKeys("n"), key.WithHelp("n/esc", "keep it")),
}

// editRefusal says why m can't be edited at now, or "" if it can.
func editRefusal(m wa.Message, now time.Time) string {
	switch {
	case !m.FromMe:
		return "You can only edit your own messages."
	case m.Kind != wa.KindText:
		return "Only text messages can be edited."
	case m.ReplyTo != nil:
		return "Editing replies isn't supported yet."
	case now.Sub(m.Timestamp) > wa.EditWindow:
		return "WhatsApp only allows edits within 15 minutes of sending."
	}
	return ""
}

// isEditKey reports an "E" however the terminal sends it: as typed text,
// or (with the kitty keyboard protocol) as shift+e.
func isEditKey(msg tea.KeyPressMsg) bool {
	return msg.Text == "E" || msg.String() == "shift+e"
}

// startEdit puts the picked message's text in the box to edit, keeping
// what was typed for afterwards, or says why it can't be edited. The text
// goes in raw, mentions as numbers, so they still work when sent.
func (m Model) startEdit() (Model, tea.Cmd) {
	target, ok := m.conv.pickedMessage()
	if !ok {
		return m, nil
	}
	if why := editRefusal(target, time.Now()); why != "" {
		m.sayScoped(why)
		return m, nil
	}
	c := &m.conv
	c.stopPicking()
	c.setReply(nil)
	c.setEditing(nil) // an edit already open: its text isn't what was typed
	c.stash = c.input.Value()
	c.input.SetValue(target.Text)
	c.input.CursorEnd()
	c.setEditing(&target)
	return m, nil
}

// setEditing starts (or, with nil, ends) an edit; the banner takes a line
// from the message pane. Ending one puts back what was typed before it.
func (c *conversation) setEditing(target *wa.Message) {
	if target == nil && c.editing != nil {
		c.input.SetValue(c.stash)
		c.input.CursorEnd()
		c.stash = ""
	}
	c.editing = target
	c.resize(c.width, c.height)
}

// editBanner is the line above the input while editing.
func editBanner() string {
	return faintStyle.Render("✎ Editing your message  (esc cancels)")
}

// sendEdit sends text as the edit of the message being edited, unless it's
// unchanged. Past the window, the text stays in the box to send as a new
// message instead.
func (m Model) sendEdit(text string) (Model, tea.Cmd) {
	c := &m.conv
	target := *c.editing
	if editRefusal(target, time.Now()) != "" {
		c.editing, c.stash = nil, ""
		c.input.SetValue(text)
		c.input.CursorEnd()
		c.resize(c.width, c.height)
		m.say("Too late to edit (WhatsApp allows 15 minutes). Your text is still in the box to send as a new message.")
		return m, nil
	}
	c.setEditing(nil)
	if text == target.Text {
		return m, nil
	}
	return m, editMessage(m.ctx, m.deps.Client, c.chat.JID, target, text)
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
		if c.editing != nil && c.editing.ID == target.ID {
			c.setEditing(nil) // nothing left to edit
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

func editMessage(ctx context.Context, client wa.Client, chat string, target wa.Message, text string) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: client.SendEdit(ctx, chat, target, text)}
	}
}

func deleteMessage(ctx context.Context, client wa.Client, chat string, target wa.Message) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: client.SendDelete(ctx, chat, target)}
	}
}

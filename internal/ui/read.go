package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// readDwell is how long a chat must stay on screen (with the terminal
// focused) before it counts as read. Arrowing past chats never marks them
// read, which matters when mark_read sends receipts.
var readDwell = 1500 * time.Millisecond // shortened in tests

type (
	readTickMsg struct {
		chat string
		seq  int
	}
	readDoneMsg struct{ err error }
)

// scheduleRead starts the dwell timer for the open chat. Any later call
// supersedes it (seq), so only a chat that stays put gets marked.
func (m *Model) scheduleRead() tea.Cmd {
	m.readSeq++
	chat, seq := m.conv.chat.JID, m.readSeq
	if chat == "" || m.draft != "" {
		return nil
	}
	return tea.Tick(readDwell, func(time.Time) tea.Msg { return readTickMsg{chat: chat, seq: seq} })
}

func (m Model) readTick(msg readTickMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.readSeq || msg.chat != m.conv.chat.JID || !m.termFocused || m.screen != screenMain || m.showHelp {
		return m, nil
	}
	if m.conv.chat.Unread == 0 {
		return m, nil
	}
	mode := wa.ReadMode(m.deps.Config.Privacy.MarkRead)
	return m, markChatRead(m.ctx, m.deps, msg.chat, mode)
}

// markChatRead clears the chat's unread messages locally and, per mode,
// tells the user's devices or the senders.
func markChatRead(ctx context.Context, deps Deps, chat string, mode wa.ReadMode) tea.Cmd {
	return func() tea.Msg {
		unread, err := deps.Store.UnreadMessages(ctx, chat)
		if err != nil || len(unread) == 0 {
			return readDoneMsg{err: err}
		}
		if err := deps.Store.MarkRead(ctx, chat, unread[len(unread)-1].Timestamp); err != nil {
			return readDoneMsg{err: err}
		}
		return readDoneMsg{err: deps.Client.MarkRead(ctx, chat, unread, mode)}
	}
}

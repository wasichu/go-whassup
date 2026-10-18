package ui

import (
	"context"
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// Store is the read side of the message store the UI needs.
type Store interface {
	Chats(ctx context.Context) ([]store.ChatSummary, error)
	Messages(ctx context.Context, chat string, limit int) ([]wa.Message, error)
}

// Deps are the UI's collaborators, wired by internal/app.
type Deps struct {
	Client wa.Client
	Store  Store
	Log    *slog.Logger

	Config     config.Config
	ConfigPath string
	// ApplyConfig makes a saved config take effect in the running app
	// (retention, pairing options). Called off the UI goroutine.
	ApplyConfig func(context.Context, config.Config) error

	// TerminalGraphics reports Kitty graphics support (SupportsKittyGraphics);
	// inline images are used when this and Config.UI.InlineImages are set.
	TerminalGraphics bool
}

// messagesPerChat is how many recent messages the conversation pane loads.
const messagesPerChat = 500

// Messages produced by commands. Commands run off the UI goroutine, so they
// close over values (ctx, deps, chat keys), never the model.
type (
	eventMsg       struct{ evt wa.Event }
	chatsLoadedMsg struct {
		chats []store.ChatSummary
		err   error
	}
	messagesLoadedMsg struct {
		chat string
		msgs []wa.Message
		err  error
	}
	connectResultMsg struct{ err error }
	sendResultMsg    struct{ err error }
)

// waitEvent blocks for the next protocol event. Update re-issues it after
// each event, so exactly one is pending at a time.
func waitEvent(ctx context.Context, sub *wa.Subscription) tea.Cmd {
	return func() tea.Msg {
		select {
		case evt := <-sub.C:
			return eventMsg{evt: evt}
		case <-ctx.Done():
			return nil
		}
	}
}

func loadChats(ctx context.Context, st Store) tea.Cmd {
	return func() tea.Msg {
		chats, err := st.Chats(ctx)
		return chatsLoadedMsg{chats: chats, err: err}
	}
}

func loadMessages(ctx context.Context, st Store, chat string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := st.Messages(ctx, chat, messagesPerChat)
		return messagesLoadedMsg{chat: chat, msgs: msgs, err: err}
	}
}

func connect(ctx context.Context, client wa.Client) tea.Cmd {
	return func() tea.Msg {
		return connectResultMsg{err: client.Connect(ctx)}
	}
}

func send(ctx context.Context, client wa.Client, chat, text string) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: client.SendText(ctx, chat, text)}
	}
}

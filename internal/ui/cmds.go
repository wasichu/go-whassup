package ui

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/wa"
)

// Store is what the UI needs from the message store: reads, plus the
// user's own read state.
type Store interface {
	Chats(ctx context.Context) ([]store.ChatSummary, error)
	Messages(ctx context.Context, chat string, limit int) ([]wa.Message, error)
	Directory(ctx context.Context) ([]store.ChatSummary, error)
	UnreadMessages(ctx context.Context, chat string) ([]wa.Message, error)
	Details(ctx context.Context, chat string) (store.ChatDetails, error)
	Names(ctx context.Context, jids []string) (map[string]string, error)
	Search(ctx context.Context, query string, limit int) ([]store.SearchHit, error)
	MarkRead(ctx context.Context, chat string, at time.Time) error
}

// MediaCache is what the UI needs from internal/media's Cache.
type MediaCache interface {
	Lookup(chat, id string) (string, bool)
	Fetch(ctx context.Context, d media.Downloader, chat, id string, m wa.Media) (string, error)
	Put(chat, id, src string, m wa.Media) error
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

	// Graphics is the terminal's inline-image support (termcaps); images are
	// used when it allows and Config.UI.InlineImages is set.
	Graphics termcaps.Graphics
	// Media is the attachment cache; OpenFile hands a file to the system
	// viewer (media.Open). Both optional: without them media can't be opened.
	Media    MediaCache
	OpenFile func(path string) error

	// Notice, if set, is shown in the status line at startup (e.g. that
	// stored messages are being encrypted in the background).
	Notice string

	// FocusHint, if set, explains why focus tracking won't work (tmux);
	// shown once in the status line.
	FocusHint string
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
		chat     string
		msgs     []wa.Message
		cached   map[string]bool   // message IDs whose attachment is on disk
		mentions map[string]string // phone digits -> name, for @mentions
		err      error
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

// loadMessages reads a chat's messages and, off the UI goroutine, which of
// their attachments are already cached (View must not touch the disk).
func loadMessages(ctx context.Context, deps Deps, chat string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := deps.Store.Messages(ctx, chat, messagesPerChat)
		mentions, nerr := mentionNames(ctx, deps.Store, msgs)
		return messagesLoadedMsg{
			chat: chat, msgs: msgs, cached: cachedFlags(deps.Media, chat, msgs), mentions: mentions,
			err: errors.Join(err, nerr),
		}
	}
}

// cachedFlags marks which messages' attachments are already downloaded.
func cachedFlags(cache MediaCache, chat string, msgs []wa.Message) map[string]bool {
	cached := map[string]bool{}
	if cache == nil {
		return cached
	}
	for _, m := range msgs {
		if m.Media == nil {
			continue
		}
		if _, ok := cache.Lookup(chat, m.ID); ok {
			cached[m.ID] = true
		}
	}
	return cached
}

// mentionNames maps the phone digits @-mentioned in msgs to contact names.
func mentionNames(ctx context.Context, st Store, msgs []wa.Message) (map[string]string, error) {
	mentions := map[string]string{}
	digits := mentionedDigits(msgs)
	if len(digits) == 0 {
		return mentions, nil
	}
	jids := make([]string, len(digits))
	for i, d := range digits {
		jids[i] = d + "@s.whatsapp.net"
	}
	names, err := st.Names(ctx, jids)
	for jid, name := range names {
		user, _, _ := strings.Cut(jid, "@")
		mentions[user] = name
	}
	return mentions, err
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

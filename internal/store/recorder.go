package store

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Recorder persists events from a wa.Client. It must be registered directly
// with AddEventHandler (not through a lossy wa.Subscription): it writes
// synchronously so no message is dropped.
type Recorder struct {
	Store     *Store
	Retention Retention // initial policy; change it later with SetRetention
	Log       *slog.Logger
	// Media, if set, has cached attachments removed when their message is
	// deleted or pruned, so "deleted" means gone from disk.
	Media MediaCache
	Now   func() time.Time // for tests; nil means time.Now

	mu sync.Mutex // guards Retention after Attach
}

// MediaCache is the part of the media cache the recorder needs.
type MediaCache interface {
	Remove(chat, id string) error
}

// SetRetention changes the policy; it is safe while events are flowing.
func (r *Recorder) SetRetention(ret Retention) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Retention = ret
}

// Attach registers the recorder on client and returns a function that
// detaches it.
func (r *Recorder) Attach(client wa.Client) (remove func()) {
	return client.AddEventHandler(r.Handle)
}

// Handle stores one event. Errors are logged: there is no caller to return
// them to on whatsmeow's goroutine.
func (r *Recorder) Handle(evt wa.Event) {
	// whatsmeow gives handlers no context; writes are local and short.
	ctx := context.Background()
	switch v := evt.(type) {
	case wa.Message:
		r.saveMessages(ctx, []wa.Message{v})
		if v.FromMe {
			// Replying means you've read the chat, as on the phone.
			r.markRead(ctx, v.Chat, v.Timestamp)
		}
	case wa.ChatRead:
		r.markRead(ctx, v.Chat, v.At)
	case wa.MessageEdited:
		found, err := r.Store.EditMessage(ctx, v)
		switch {
		case err != nil:
			r.Log.Error("recording edit", "id", v.ID, "err", err)
		case !found:
			// IDs only: never log message text by default.
			r.Log.Info("edit for a message we don't have (or an older edit)", "chat", v.Chat, "id", v.ID)
		default:
			r.Log.Info("applied edit", "chat", v.Chat, "id", v.ID)
		}
	case wa.MessageDeleted:
		if err := r.Store.DeleteMessage(ctx, v); err != nil {
			r.Log.Error("recording delete", "id", v.ID, "err", err)
		}
		r.removeMedia(v.Chat, v.ID)
	case wa.Reaction:
		if err := r.Store.SaveReactions(ctx, []wa.Reaction{v}); err != nil {
			r.Log.Error("recording reaction", "id", v.MessageID, "err", err)
		}
	case wa.Contacts:
		if err := r.Store.SaveContacts(ctx, v.List); err != nil {
			r.Log.Error("recording contacts", "count", len(v.List), "err", err)
		}
	case wa.HistorySync:
		if err := r.Store.SaveChats(ctx, v.Chats); err != nil {
			r.Log.Error("recording history chats", "err", err)
		}
		if err := r.Store.SavePushNames(ctx, v.PushNames); err != nil {
			r.Log.Error("recording push names", "err", err)
		}
		r.saveMessages(ctx, v.Messages)
		if err := r.Store.SaveReactions(ctx, v.Reactions); err != nil {
			r.Log.Error("recording history reactions", "err", err)
		}
		for _, c := range v.Chats {
			if err := r.Store.SetUnreadFromPhone(ctx, c.JID, c.UnreadCount); err != nil {
				r.Log.Error("recording unread count", "chat", c.JID, "err", err)
			}
		}
		r.Prune(ctx)
	}
}

// Prune applies the retention policy now.
func (r *Recorder) Prune(ctx context.Context) {
	r.mu.Lock()
	ret := r.Retention
	r.mu.Unlock()
	if ret == (Retention{}) {
		return
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	removed, err := r.Store.Prune(ctx, ret, now())
	if err != nil {
		r.Log.Error("pruning messages", "err", err)
		return
	}
	for _, ref := range removed {
		r.removeMedia(ref.Chat, ref.ID)
	}
	if len(removed) > 0 {
		r.Log.Info("pruned messages", "count", len(removed))
	}
}

func (r *Recorder) removeMedia(chat, id string) {
	if r.Media == nil {
		return
	}
	if err := r.Media.Remove(chat, id); err != nil {
		r.Log.Error("removing cached media", "chat", chat, "id", id, "err", err)
	}
}

func (r *Recorder) markRead(ctx context.Context, chat string, at time.Time) {
	if err := r.Store.MarkRead(ctx, chat, at); err != nil {
		r.Log.Error("recording read state", "chat", chat, "err", err)
	}
}

func (r *Recorder) saveMessages(ctx context.Context, msgs []wa.Message) {
	keep := make([]wa.Message, 0, len(msgs))
	names := map[string]string{}
	for _, m := range msgs {
		// Reactions arrive as wa.Reaction events; never store one as a message.
		if m.Kind == wa.KindReaction {
			continue
		}
		keep = append(keep, m)
		if !m.FromMe && m.PushName != "" {
			names[m.Sender] = m.PushName
		}
	}
	n, err := r.Store.SaveMessages(ctx, keep)
	if err != nil {
		r.Log.Error("recording messages", "count", len(keep), "err", err)
		return
	}
	if err := r.Store.SavePushNames(ctx, names); err != nil {
		r.Log.Error("recording push names", "err", err)
	}
	r.Log.Debug("recorded messages", "new", n, "received", len(msgs))
}

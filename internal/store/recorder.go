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

	mu     sync.Mutex // guards Retention after Attach
	client wa.Client  // set by Attach: looks up privacy IDs
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
	r.client = client
	return client.AddEventHandler(r.Handle)
}

// Handle stores one event. Errors are logged: there is no caller to return
// them to on whatsmeow's goroutine.
func (r *Recorder) Handle(evt wa.Event) {
	// whatsmeow gives handlers no context; writes are local and short.
	ctx := context.Background()
	switch v := evt.(type) {
	case wa.Message:
		r.onMessage(ctx, v)
	case wa.ChatRead:
		r.markRead(ctx, v.Chat, v.At)
	case wa.MessageEdited:
		r.onEdit(ctx, v)
	case wa.MessageDeleted:
		r.logIf(r.Store.DeleteMessage(ctx, v), "recording delete", "id", v.ID)
		r.removeMedia(v.Chat, v.ID)
	case wa.Reaction:
		r.logIf(r.Store.SaveReactions(ctx, []wa.Reaction{v}), "recording reaction", "id", v.MessageID)
	case wa.Groups:
		r.logIf(r.Store.SaveGroups(ctx, v.List), "recording groups", "count", len(v.List))
		r.resolvePrivacyIDs(ctx)
	case wa.Contacts:
		r.logIf(r.Store.SaveContacts(ctx, v.List), "recording contacts", "count", len(v.List))
		r.resolvePrivacyIDs(ctx)
	case wa.ChatMuted:
		r.logIf(r.Store.SetMuted(ctx, v.Chat, v.Until), "recording mute", "chat", v.Chat)
	case wa.HistorySync:
		r.onHistory(ctx, v)
		r.resolvePrivacyIDs(ctx)
	}
}

// resolvePrivacyIDs records the numbers of stored privacy IDs that WhatsApp
// has revealed since, so their names show. It runs on the events that
// bring names (contacts, groups, history), before the UI reloads for them.
func (r *Recorder) resolvePrivacyIDs(ctx context.Context) {
	if r.client == nil {
		return
	}
	lids, err := r.Store.UnresolvedPrivacyIDs(ctx)
	if err != nil || len(lids) == 0 {
		r.logIf(err, "listing privacy IDs")
		return
	}
	found, err := r.client.PhoneNumbers(ctx, lids)
	r.logIf(err, "looking up privacy IDs", "count", len(lids))
	if len(found) > 0 {
		r.logIf(r.Store.SaveLIDMap(ctx, found), "saving privacy IDs", "count", len(found))
	}
}

// logIf logs err, if any: on whatsmeow's goroutine there's no caller to
// return it to.
func (r *Recorder) logIf(err error, what string, attrs ...any) {
	if err != nil {
		r.Log.Error(what, append(attrs, "err", err)...)
	}
}

func (r *Recorder) onMessage(ctx context.Context, m wa.Message) {
	r.saveMessages(ctx, []wa.Message{m})
	if m.FromMe {
		// Replying means you've read the chat, as on the phone.
		r.markRead(ctx, m.Chat, m.Timestamp)
	}
}

func (r *Recorder) onEdit(ctx context.Context, e wa.MessageEdited) {
	found, err := r.Store.EditMessage(ctx, e)
	switch {
	case err != nil:
		r.Log.Error("recording edit", "id", e.ID, "err", err)
	case !found:
		// IDs only: never log message text by default.
		r.Log.Info("edit for a message we don't have (or an older edit)", "chat", e.Chat, "id", e.ID)
	default:
		r.Log.Info("applied edit", "chat", e.Chat, "id", e.ID)
	}
}

func (r *Recorder) onHistory(ctx context.Context, h wa.HistorySync) {
	r.logIf(r.Store.SaveChats(ctx, h.Chats), "recording history chats")
	r.logIf(r.Store.SavePushNames(ctx, h.PushNames), "recording push names")
	r.saveMessages(ctx, h.Messages)
	r.logIf(r.Store.SaveReactions(ctx, h.Reactions), "recording history reactions")
	for _, c := range h.Chats {
		r.logIf(r.Store.SetUnreadFromPhone(ctx, c.JID, c.UnreadCount), "recording unread count", "chat", c.JID)
	}
	r.Prune(ctx)
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

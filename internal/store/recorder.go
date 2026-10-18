package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Recorder persists events from a wa.Client. It must be registered directly
// with AddEventHandler (not through a lossy wa.Subscription): it writes
// synchronously so no message is dropped.
type Recorder struct {
	Store     *Store
	Retention Retention
	Log       *slog.Logger
	Now       func() time.Time // for tests; nil means time.Now
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
	case wa.HistorySync:
		if err := r.Store.SaveChats(ctx, v.Chats); err != nil {
			r.Log.Error("recording history chats", "err", err)
		}
		if err := r.Store.SavePushNames(ctx, v.PushNames); err != nil {
			r.Log.Error("recording push names", "err", err)
		}
		r.saveMessages(ctx, v.Messages)
		r.Prune(ctx)
	}
}

// Prune applies the retention policy now.
func (r *Recorder) Prune(ctx context.Context) {
	if r.Retention == (Retention{}) {
		return
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	n, err := r.Store.Prune(ctx, r.Retention, now())
	if err != nil {
		r.Log.Error("pruning messages", "err", err)
		return
	}
	if n > 0 {
		r.Log.Info("pruned messages", "count", n)
	}
}

func (r *Recorder) saveMessages(ctx context.Context, msgs []wa.Message) {
	keep := make([]wa.Message, 0, len(msgs))
	names := map[string]string{}
	for _, m := range msgs {
		// Reactions would otherwise show up as messages and bump chat
		// activity; they'll be attached to their targets in a later step.
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

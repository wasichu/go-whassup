package wa

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// announceName tells the server our push name so recipients don't see "-"
// as the sender. It uses "unavailable" so this doesn't mark the user online
// or switch on active read receipts.
func (w *WhatsApp) announceName() {
	err := w.client().SendPresence(context.Background(), types.PresenceUnavailable)
	if err != nil {
		// Right after pairing the push name may not have synced yet; the
		// next connection will succeed.
		w.log.Debug("announcing push name", "err", err)
	}
}

// decryptSecret decrypts a secret-encrypted message (e.g. an edit) with the
// stored secret of the message it targets. Failures are logged; the caller
// drops the message rather than storing it undecrypted.
func (w *WhatsApp) decryptSecret(evt *events.Message) (*waE2E.Message, error) {
	msg, err := w.client().DecryptSecretEncryptedMessage(context.Background(), evt)
	if err != nil {
		w.log.Warn("couldn't decrypt secret-encrypted message", "id", evt.Info.ID,
			"target", evt.Message.GetSecretEncryptedMessage().GetTargetMessageKey().GetID(), "err", err)
	}
	return msg, err
}

// emitGroupSnapshot fetches all joined groups (one request) and emits them.
func (w *WhatsApp) emitGroupSnapshot() {
	w.mu.Lock()
	if time.Since(w.lastGroupSync) < groupSyncInterval {
		w.mu.Unlock()
		return
	}
	w.lastGroupSync = time.Now()
	w.mu.Unlock()

	cli := w.client()
	groups, err := cli.GetJoinedGroups(context.Background())
	if err != nil {
		w.log.Warn("fetching groups", "err", err)
		return
	}
	list := make([]Group, 0, len(groups))
	for _, g := range groups {
		list = append(list, groupFromInfo(g, w.resolveKey))
	}
	w.log.Info("groups snapshot", "count", len(list))
	w.emit(Groups{List: list, Snapshot: true})
}

// refreshGroup re-fetches one group after the server reports a change.
func (w *WhatsApp) refreshGroup(jid types.JID) {
	info, err := w.client().GetGroupInfo(context.Background(), jid)
	if err != nil {
		w.log.Warn("fetching group info", "group", jid, "err", err)
		return
	}
	w.emit(Groups{List: []Group{groupFromInfo(info, w.resolveKey)}})
}

// emitContactSnapshot emits every known contact. It reads whatsmeow's local
// store, so it is cheap and works whenever a session exists.
func (w *WhatsApp) emitContactSnapshot() {
	cli := w.client()
	if cli == nil || cli.Store.ID == nil {
		return
	}
	all, err := cli.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		w.log.Warn("reading contacts", "err", err)
		return
	}
	list := contactsFromStore(all, w.resolveKey)
	w.log.Info("contacts snapshot", "count", len(list))
	w.emit(Contacts{List: list, Snapshot: true})
}

// handleEvent runs on whatsmeow's goroutines. It translates protocol events
// into our plain Event values and must never touch UI state directly.
func (w *WhatsApp) handleEvent(evt any) {
	if w.connectionEvent(evt) || w.peopleEvent(evt) || w.messageEvent(evt) {
		return
	}
	// App-state sync, receipts, presence etc. Handled in later steps; a
	// fresh pairing produces a flood of these.
	w.log.Debug("unhandled event", "type", fmt.Sprintf("%T", evt))
}

// connectionEvent handles connection and pairing events.
func (w *WhatsApp) connectionEvent(evt any) bool {
	switch v := evt.(type) {
	case *events.Connected:
		go w.announceName() // not on whatsmeow's event goroutine: it sends a node
		w.emit(Connected{})
		go w.emitContactSnapshot()
		go w.emitGroupSnapshot()
	case *events.Disconnected:
		w.emit(Disconnected{})
	case *events.StreamReplaced:
		w.log.Warn("another client took over this session")
		w.emit(Disconnected{})
	case *events.LoggedOut:
		w.emit(LoggedOut{Reason: v.Reason.String()})
	case *events.TemporaryBan:
		w.log.Error("temporary ban", "detail", v.String())
		w.emit(Disconnected{})
	case *events.PairSuccess:
		w.emit(PairSuccess{JID: v.ID.ToNonAD().String()})
	default:
		return false
	}
	return true
}

// peopleEvent handles contacts, groups and read state.
func (w *WhatsApp) peopleEvent(evt any) bool {
	switch v := evt.(type) {
	case *events.JoinedGroup:
		w.emit(Groups{List: []Group{groupFromInfo(&v.GroupInfo, w.resolveKey)}})
	case *events.GroupInfo:
		go w.refreshGroup(v.JID) // name, topic or membership changed
	case *events.AppStateSyncComplete:
		go w.emitContactSnapshot() // address-book names arrive via app state
	case *events.Contact:
		if !v.FromFullSync { // the AppStateSyncComplete snapshot covers full syncs
			w.emit(Contacts{List: []Contact{w.contactFromAction(v)}})
		}
	case *events.PushName:
		w.emit(Contacts{List: []Contact{{JID: w.resolveKey(v.JID, v.JIDAlt), PushName: v.NewPushName}}})
	case *events.BusinessName:
		w.emit(Contacts{List: []Contact{{JID: w.resolveKey(v.JID, types.EmptyJID), BusinessName: v.NewBusinessName}}})
	case *events.MarkChatAsRead:
		if v.Action.GetRead() {
			w.emit(ChatRead{Chat: w.resolveKey(v.JID, types.EmptyJID), At: v.Timestamp})
		}
	case *events.Receipt:
		// Our own other device read an incoming message.
		if v.IsFromMe && (v.Type == types.ReceiptTypeRead || v.Type == types.ReceiptTypeReadSelf) {
			w.emit(ChatRead{Chat: w.resolveKey(v.Chat, types.EmptyJID), At: v.Timestamp})
		}
	default:
		return false
	}
	return true
}

func (w *WhatsApp) contactFromAction(v *events.Contact) Contact {
	name := v.Action.GetFullName()
	if name == "" {
		name = v.Action.GetFirstName()
	}
	var pn types.JID
	if p, err := types.ParseJID(v.Action.GetPnJID()); err == nil {
		pn = p
	}
	return Contact{JID: w.resolveKey(v.JID, pn), Name: name}
}

// messageEvent handles messages (and what acts on them) and history.
func (w *WhatsApp) messageEvent(evt any) bool {
	switch v := evt.(type) {
	case *events.Message:
		if e, ok := classifyMessage(v, w.resolveKey, w.decryptSecret); ok {
			w.emit(e)
		}
	case *events.DeleteForMe:
		w.emit(MessageDeleted{Chat: w.resolveKey(v.ChatJID, types.EmptyJID), ID: v.MessageID, At: v.Timestamp, ForMe: true})
	case *events.HistorySync:
		if h, ok := historyFromProto(v.Data, w.client().ParseWebMessage, w.resolveKey, w.log); ok {
			w.log.Info("history sync", "type", v.Data.GetSyncType().String(),
				"chats", len(h.Chats), "messages", len(h.Messages), "push_names", len(h.PushNames))
			w.emit(h)
		}
	default:
		return false
	}
	return true
}

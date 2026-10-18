package wa

import (
	"log/slog"
	"slices"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// keyResolver turns a JID into the app's stable key for a user or chat. alt
// is the alternate address whatsmeow supplies alongside it, if any.
//
// WhatsApp addresses some users by a privacy ID ("…@lid") instead of their
// phone number. Keys prefer the phone-number form so one person maps to one
// chat; an unresolvable LID is kept as-is rather than dropped.
type keyResolver func(jid, alt types.JID) string

// messageFromEvent translates a whatsmeow message. ok is false for messages
// we don't surface: protocol messages and status updates (stories).
func messageFromEvent(evt *events.Message, resolve keyResolver) (Message, bool) {
	if evt.Info.Chat == types.StatusBroadcastJID {
		return Message{}, false
	}
	kind, text, ok := messageContent(evt.Message)
	if !ok {
		return Message{}, false
	}
	// In a LID-addressed DM the phone-number form of the chat is the other
	// party's: the recipient for our own messages, the sender otherwise.
	chatAlt := evt.Info.SenderAlt
	if evt.Info.IsFromMe {
		chatAlt = evt.Info.RecipientAlt
	}
	if evt.Info.IsGroup {
		chatAlt = types.EmptyJID
	}
	return Message{
		ID:        evt.Info.ID,
		Chat:      resolve(evt.Info.Chat, chatAlt),
		Sender:    resolve(evt.Info.Sender, evt.Info.SenderAlt),
		PushName:  evt.Info.PushName,
		Timestamp: evt.Info.Timestamp,
		FromMe:    evt.Info.IsFromMe,
		IsGroup:   evt.Info.IsGroup,
		Kind:      kind,
		Text:      text,
	}, true
}

// webMessageParser parses a history message; it is Client.ParseWebMessage.
type webMessageParser func(chat types.JID, msg *waWeb.WebMessageInfo) (*events.Message, error)

// historyFromProto translates a history sync blob. ok is false for blobs we
// ignore entirely (status/stories bootstrap) or that turn out empty.
func historyFromProto(data *waHistorySync.HistorySync, parse webMessageParser, resolve keyResolver, log *slog.Logger) (HistorySync, bool) {
	if data == nil || data.GetSyncType() == waHistorySync.HistorySync_INITIAL_STATUS_V3 {
		return HistorySync{}, false
	}
	var h HistorySync
	if names := data.GetPushnames(); len(names) > 0 {
		h.PushNames = make(map[string]string, len(names))
		for _, pn := range names {
			jid, err := types.ParseJID(pn.GetID())
			if err != nil || pn.GetPushname() == "" {
				continue
			}
			h.PushNames[resolve(jid, types.EmptyJID)] = pn.GetPushname()
		}
	}

	for _, conv := range data.GetConversations() {
		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			log.Debug("skipping history conversation with bad id", "id", conv.GetID(), "err", err)
			continue
		}
		if chatJID == types.StatusBroadcastJID {
			continue
		}
		var pnAlt types.JID
		if pn, err := types.ParseJID(conv.GetPnJID()); err == nil {
			pnAlt = pn
		}
		chatKey := resolve(chatJID, pnAlt)
		name := conv.GetName()
		if name == "" {
			name = conv.GetDisplayName()
		}
		h.Chats = append(h.Chats, Chat{
			JID: chatKey, Name: name, IsGroup: chatJID.Server == types.GroupServer,
			UnreadCount: int(conv.GetUnreadCount()),
		})

		for _, hm := range conv.GetMessages() {
			evt, err := parse(chatJID, hm.GetMessage())
			if err != nil {
				log.Debug("skipping unparseable history message", "chat", chatKey, "err", err)
				continue
			}
			msg, ok := messageFromEvent(evt, resolve)
			if !ok {
				continue
			}
			msg.Chat = chatKey // the conversation's key is authoritative
			h.Messages = append(h.Messages, msg)
		}
	}
	empty := len(h.Chats) == 0 && len(h.Messages) == 0 && len(h.PushNames) == 0
	return h, !empty
}

// contactsFromStore translates whatsmeow's contact store snapshot, merging
// entries that resolve to the same key (a LID and its phone number) so the
// most complete names win.
func contactsFromStore(all map[types.JID]types.ContactInfo, resolve keyResolver) []Contact {
	merged := map[string]*Contact{}
	var order []string
	for jid, info := range all {
		if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
			continue
		}
		key := resolve(jid, types.EmptyJID)
		c, ok := merged[key]
		if !ok {
			c = &Contact{JID: key}
			merged[key] = c
			order = append(order, key)
		}
		name := info.FullName
		if name == "" {
			name = info.FirstName
		}
		c.Name = firstNonEmpty(c.Name, name)
		c.PushName = firstNonEmpty(c.PushName, info.PushName)
		c.BusinessName = firstNonEmpty(c.BusinessName, info.BusinessName)
	}
	slices.Sort(order) // deterministic output for tests and diffs
	out := make([]Contact, 0, len(order))
	for _, k := range order {
		if c := merged[k]; c.Name != "" || c.PushName != "" || c.BusinessName != "" {
			out = append(out, *c)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

package wa

import (
	"log/slog"
	"slices"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"

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
	if !ok || kind == KindReaction {
		return Message{}, false // reactions are Reaction events, not messages
	}
	m, _ := messageFromEventBase(evt, resolve)
	m.Kind, m.Text = kind, text
	return m, true
}

// secretDecrypter decrypts a SecretEncryptedMessage; it is
// Client.DecryptSecretEncryptedMessage.
type secretDecrypter func(*events.Message) (*waE2E.Message, error)

// classifyMessage turns a whatsmeow message into the event it represents:
// a Message, or an edit, revoke or reaction referring to an earlier one.
func classifyMessage(evt *events.Message, resolve keyResolver, decrypt secretDecrypter) (Event, bool) {
	if evt.Info.Chat == types.StatusBroadcastJID {
		return nil, false
	}
	base, _ := messageFromEventBase(evt, resolve)
	if sem := evt.Message.GetSecretEncryptedMessage(); sem != nil {
		// Newer clients send edits encrypted with the original message's
		// secret. Anything else secret-encrypted (poll/event edits) isn't
		// shown, and is never a message in its own right.
		if sem.GetSecretEncType() != waE2E.SecretEncryptedMessage_MESSAGE_EDIT || sem.GetTargetMessageKey().GetID() == "" {
			return nil, false
		}
		plain, err := decrypt(evt)
		if err != nil {
			return nil, false
		}
		edited := plain
		if p := plain.GetProtocolMessage(); p.GetEditedMessage() != nil {
			edited = p.GetEditedMessage()
		}
		kind, text, ok := messageContent(edited)
		if !ok {
			return nil, false
		}
		return MessageEdited{Chat: base.Chat, ID: sem.GetTargetMessageKey().GetID(), Kind: kind, Text: text, At: base.Timestamp}, true
	}
	if r := evt.Message.GetReactionMessage(); r != nil && r.GetKey().GetID() != "" {
		at := base.Timestamp
		if ms := r.GetSenderTimestampMS(); ms > 0 {
			at = time.UnixMilli(ms)
		}
		return Reaction{
			Chat: base.Chat, MessageID: r.GetKey().GetID(),
			Sender: base.Sender, FromMe: base.FromMe, Emoji: r.GetText(), At: at,
		}, true
	}
	if p := evt.Message.GetProtocolMessage(); p != nil {
		// Type's zero value is REVOKE, so only trust a type that came with a
		// target message.
		if p.Type == nil || p.GetKey().GetID() == "" {
			return nil, false
		}
		switch p.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			return MessageDeleted{Chat: base.Chat, ID: p.GetKey().GetID(), At: base.Timestamp}, true
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			kind, text, ok := messageContent(p.GetEditedMessage())
			if !ok {
				return nil, false
			}
			at := base.Timestamp
			if ms := p.GetTimestampMS(); ms > 0 {
				at = time.UnixMilli(ms)
			}
			return MessageEdited{Chat: base.Chat, ID: p.GetKey().GetID(), Kind: kind, Text: text, At: at}, true
		}
		return nil, false
	}
	return messageFromEvent(evt, resolve)
}

// messageFromEventBase fills the addressing fields shared by every kind of
// message, without looking at the content.
func messageFromEventBase(evt *events.Message, resolve keyResolver) (Message, bool) {
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
			web := hm.GetMessage()
			for _, r := range web.GetReactions() {
				sender := chatJID // in DMs the reactor is the other party unless it's us
				fromMe := r.GetKey().GetFromMe()
				if p, err := types.ParseJID(r.GetKey().GetParticipant()); err == nil && !p.IsEmpty() {
					sender = p
				}
				h.Reactions = append(h.Reactions, Reaction{
					Chat: chatKey, MessageID: web.GetKey().GetID(),
					Sender: resolve(sender, types.EmptyJID), FromMe: fromMe,
					Emoji: r.GetText(), At: time.UnixMilli(r.GetSenderTimestampMS()),
				})
			}
			evt, err := parse(chatJID, web)
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
	empty := len(h.Chats) == 0 && len(h.Messages) == 0 && len(h.PushNames) == 0 && len(h.Reactions) == 0
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

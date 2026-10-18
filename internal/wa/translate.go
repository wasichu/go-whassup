package wa

import (
	"log/slog"
	"slices"
	"strings"
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
	m.Kind, m.Text, m.Media = kind, normalizeMentions(text, evt.Message, resolve), mediaOf(evt.Message)
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
		return classifySecretEdit(evt, sem, base, decrypt)
	}
	if r := evt.Message.GetReactionMessage(); r != nil && r.GetKey().GetID() != "" {
		return classifyReaction(r, base), true
	}
	if p := evt.Message.GetProtocolMessage(); p != nil {
		return classifyProtocol(p, base)
	}
	return messageFromEvent(evt, resolve)
}

// classifySecretEdit handles secret-encrypted messages. Newer clients send
// edits encrypted with the original message's secret; anything else
// secret-encrypted (poll/event edits) isn't shown and is never a message
// in its own right.
func classifySecretEdit(evt *events.Message, sem *waE2E.SecretEncryptedMessage, base Message, decrypt secretDecrypter) (Event, bool) {
	target := sem.GetTargetMessageKey().GetID()
	if sem.GetSecretEncType() != waE2E.SecretEncryptedMessage_MESSAGE_EDIT || target == "" {
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
	return MessageEdited{Chat: base.Chat, ID: target, Kind: kind, Text: text, At: base.Timestamp}, true
}

func classifyReaction(r *waE2E.ReactionMessage, base Message) Reaction {
	return Reaction{
		Chat: base.Chat, MessageID: r.GetKey().GetID(),
		Sender: base.Sender, FromMe: base.FromMe, Emoji: r.GetText(),
		At: atOr(r.GetSenderTimestampMS(), base.Timestamp),
	}
}

// classifyProtocol handles revokes and plain (unencrypted) edits.
func classifyProtocol(p *waE2E.ProtocolMessage, base Message) (Event, bool) {
	// Type's zero value is REVOKE, so only trust a type that came with a
	// target message.
	target := p.GetKey().GetID()
	if p.Type == nil || target == "" {
		return nil, false
	}
	switch p.GetType() {
	case waE2E.ProtocolMessage_REVOKE:
		return MessageDeleted{Chat: base.Chat, ID: target, At: base.Timestamp}, true
	case waE2E.ProtocolMessage_MESSAGE_EDIT:
		kind, text, ok := messageContent(p.GetEditedMessage())
		if !ok {
			return nil, false
		}
		return MessageEdited{Chat: base.Chat, ID: target, Kind: kind, Text: text, At: atOr(p.GetTimestampMS(), base.Timestamp)}, true
	}
	return nil, false
}

// atOr is the sender's timestamp (milliseconds) if set, else fallback.
func atOr(ms int64, fallback time.Time) time.Time {
	if ms > 0 {
		return time.UnixMilli(ms)
	}
	return fallback
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
	h := HistorySync{PushNames: historyPushNames(data.GetPushnames(), resolve)}
	for _, conv := range data.GetConversations() {
		addConversation(&h, conv, parse, resolve, log)
	}
	empty := len(h.Chats) == 0 && len(h.Messages) == 0 && len(h.PushNames) == 0 && len(h.Reactions) == 0
	return h, !empty
}

func historyPushNames(names []*waHistorySync.Pushname, resolve keyResolver) map[string]string {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, pn := range names {
		jid, err := types.ParseJID(pn.GetID())
		if err != nil || pn.GetPushname() == "" {
			continue
		}
		out[resolve(jid, types.EmptyJID)] = pn.GetPushname()
	}
	return out
}

// addConversation adds one history conversation's chat, messages and
// reactions to h.
func addConversation(h *HistorySync, conv *waHistorySync.Conversation, parse webMessageParser, resolve keyResolver, log *slog.Logger) {
	chatJID, err := types.ParseJID(conv.GetID())
	if err != nil {
		log.Debug("skipping history conversation with bad id", "id", conv.GetID(), "err", err)
		return
	}
	if chatJID == types.StatusBroadcastJID {
		return
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
		MutedUntil:  muteEnd(int64(conv.GetMuteEndTime())), // negative (e.g. -1 stored as a uint64) = forever
	})
	for _, hm := range conv.GetMessages() {
		web := hm.GetMessage()
		h.Reactions = append(h.Reactions, historyReactions(web, chatJID, chatKey, resolve)...)
		evt, err := parse(chatJID, web)
		if err != nil {
			log.Debug("skipping unparseable history message", "chat", chatKey, "err", err)
			continue
		}
		if msg, ok := messageFromEvent(evt, resolve); ok {
			msg.Chat = chatKey // the conversation's key is authoritative
			h.Messages = append(h.Messages, msg)
		}
	}
}

// historyReactions translates the reactions attached to a history message.
func historyReactions(web *waWeb.WebMessageInfo, chatJID types.JID, chatKey string, resolve keyResolver) []Reaction {
	var out []Reaction
	for _, r := range web.GetReactions() {
		sender := chatJID // in DMs the reactor is the other party unless it's us
		if p, err := types.ParseJID(r.GetKey().GetParticipant()); err == nil && !p.IsEmpty() {
			sender = p
		}
		out = append(out, Reaction{
			Chat: chatKey, MessageID: web.GetKey().GetID(),
			Sender: resolve(sender, types.EmptyJID), FromMe: r.GetKey().GetFromMe(),
			Emoji: r.GetText(), At: time.UnixMilli(r.GetSenderTimestampMS()),
		})
	}
	return out
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

// groupFromInfo translates whatsmeow's group info, keying participants by
// phone number where known.
func groupFromInfo(info *types.GroupInfo, resolve keyResolver) Group {
	g := Group{
		JID:          info.JID.ToNonAD().String(),
		Name:         info.Name,
		Topic:        info.Topic,
		Participants: make([]Participant, 0, len(info.Participants)),
	}
	for _, p := range info.Participants {
		g.Participants = append(g.Participants, Participant{
			JID:   resolve(p.JID, p.PhoneNumber),
			Admin: p.IsAdmin || p.IsSuperAdmin,
		})
	}
	return g
}

// contextInfo returns the ContextInfo of messages that can mention people.
func contextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	}
	return nil
}

// normalizeMentions rewrites "@<lid digits>" mentions in text to
// "@<phone digits>" when the mentioned privacy ID resolves to a number, so
// stored text always mentions people the way contacts are keyed.
func normalizeMentions(text string, msg *waE2E.Message, resolve keyResolver) string {
	for _, raw := range contextInfo(msg).GetMentionedJID() {
		jid, err := types.ParseJID(raw)
		if err != nil || jid.Server != types.HiddenUserServer {
			continue
		}
		key, err := types.ParseJID(resolve(jid, types.EmptyJID))
		if err != nil || key.Server != types.DefaultUserServer {
			continue
		}
		text = strings.ReplaceAll(text, "@"+jid.User, "@"+key.User)
	}
	return text
}

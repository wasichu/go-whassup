package wa

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Replies and reactions point at an earlier message. They use the author's
// chat key as stored (a phone-number JID). Reactions with it are confirmed
// in groups (a community's announcement group); replies in groups aren't yet.

// SendReply sends text as a reply to message to (in chat): WhatsApp shows a
// quote of to above it. Like SendText, the sent message comes back as a
// Message event.
func (w *WhatsApp) SendReply(ctx context.Context, chat, text string, to Message) error {
	return w.sendText(ctx, chat, buildReply(text, to, w.quotedAuthor(to)), text, &Reply{ID: to.ID, Sender: to.Sender})
}

// SendReaction reacts to message to with emoji ("" removes your reaction).
// whatsmeow doesn't echo what this device sends, so the reaction is emitted
// as a Reaction event once the server accepts it.
func (w *WhatsApp) SendReaction(ctx context.Context, chat string, to Message, emoji string) error {
	cli := w.client()
	if cli.Store.ID == nil {
		return ErrNotPaired
	}
	jid, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("bad chat %q: %w", chat, err)
	}
	author, err := types.ParseJID(w.quotedAuthor(to))
	if err != nil {
		return fmt.Errorf("bad author %q: %w", to.Sender, err)
	}
	resp, err := cli.SendMessage(ctx, jid, cli.BuildReaction(jid, author, to.ID, emoji))
	if err != nil {
		return fmt.Errorf("sending reaction: %w", err)
	}
	w.emit(Reaction{
		Chat: chat, MessageID: to.ID, Sender: cli.Store.ID.ToNonAD().String(), FromMe: true,
		Emoji: emoji, At: resp.Timestamp,
	})
	return nil
}

// quotedAuthor is who wrote to, as a JID string: ourselves for our own
// messages (FromMe), else the stored sender.
func (w *WhatsApp) quotedAuthor(to Message) string {
	if to.FromMe {
		if id := w.client().Store.ID; id != nil {
			return id.ToNonAD().String()
		}
	}
	return to.Sender
}

// buildReply is text as a reply to to, written by author: an extended text
// message whose ContextInfo points at to and carries a copy for the quote
// (an attachment's own message, so its thumbnail and caption show).
func buildReply(text string, to Message, author string) *waE2E.Message {
	quoted := &waE2E.Message{Conversation: proto.String(to.Text)}
	if to.Media != nil {
		var m waE2E.Message
		if err := proto.Unmarshal(to.Media.Data, &m); err == nil {
			quoted = &m
		}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String(text),
		ContextInfo: &waE2E.ContextInfo{
			StanzaID:      proto.String(to.ID),
			Participant:   proto.String(author),
			QuotedMessage: quoted,
		},
	}}
}

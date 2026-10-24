package wa

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Editing and deleting your own messages. WhatsApp only allows each for a
// while after sending. A late delete is worse than refused: the server
// accepts it but recipients' phones ignore it, so this device alone would
// show the message as deleted. Callers check the windows first.

// EditWindow is how long after sending WhatsApp lets you edit a message.
const EditWindow = 15 * time.Minute

// DeleteWindow is how long after sending "delete for everyone" works.
// WhatsApp says about two days (reported as 60 hours since 2023); this
// stays on the safe side of both.
const DeleteWindow = 48 * time.Hour

// SendEdit replaces the text of target, a text message you sent. Like
// SendReaction, it's emitted as a MessageEdited event once sent.
func (w *WhatsApp) SendEdit(ctx context.Context, chat string, target Message, text string) error {
	resp, err := w.sendProtocol(ctx, chat, func(cli *whatsmeow.Client, jid types.JID) *waE2E.Message {
		return cli.BuildEdit(jid, target.ID, &waE2E.Message{Conversation: proto.String(text)})
	})
	if err != nil {
		return fmt.Errorf("sending edit: %w", err)
	}
	w.emit(MessageEdited{Chat: chat, ID: target.ID, Kind: KindText, Text: text, At: resp.Timestamp})
	return nil
}

// SendDelete deletes target, a message you sent, for everyone: it's
// emitted as a MessageDeleted event once sent, leaving the placeholder
// everyone else sees.
func (w *WhatsApp) SendDelete(ctx context.Context, chat string, target Message) error {
	resp, err := w.sendProtocol(ctx, chat, func(cli *whatsmeow.Client, jid types.JID) *waE2E.Message {
		return cli.BuildRevoke(jid, types.EmptyJID, target.ID) // empty sender: our own message
	})
	if err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}
	w.emit(MessageDeleted{Chat: chat, ID: target.ID, At: resp.Timestamp})
	return nil
}

// sendProtocol sends the message build makes for chat.
func (w *WhatsApp) sendProtocol(ctx context.Context, chat string, build func(*whatsmeow.Client, types.JID) *waE2E.Message) (whatsmeow.SendResponse, error) {
	cli := w.client()
	if cli.Store.ID == nil {
		return whatsmeow.SendResponse{}, ErrNotPaired
	}
	jid, err := types.ParseJID(chat)
	if err != nil {
		return whatsmeow.SendResponse{}, fmt.Errorf("bad chat %q: %w", chat, err)
	}
	return cli.SendMessage(ctx, jid, build(cli, jid))
}

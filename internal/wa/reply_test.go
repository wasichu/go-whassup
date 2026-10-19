package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestBuildReply(t *testing.T) {
	orig := Message{ID: "Q1", Sender: pnAda.String(), Kind: KindText, Text: "Tea on Saturday?"}
	msg := buildReply("yes please", orig, pnAda.String())
	ext := msg.GetExtendedTextMessage()
	ci := ext.GetContextInfo()
	if ext.GetText() != "yes please" || ci.GetStanzaID() != "Q1" || ci.GetParticipant() != pnAda.String() ||
		ci.GetQuotedMessage().GetConversation() != "Tea on Saturday?" {
		t.Fatalf("reply = %v", msg)
	}

	// What we send reads back as a reply to the same message.
	evt := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: pnAda, Sender: pnMe, IsFromMe: true}, ID: "R1"}, Message: msg}
	back, ok := messageFromEvent(evt, stubResolver)
	if !ok || back.Text != "yes please" || back.ReplyTo == nil || *back.ReplyTo != (Reply{ID: "Q1", Sender: pnAda.String()}) {
		t.Errorf("round trip = %+v (ReplyTo %+v)", back, back.ReplyTo)
	}

	// Replying to a photo quotes the photo's own message (thumbnail, caption).
	photo := Message{ID: "P1", Sender: pnAda.String(), Kind: KindImage, Text: "the view", Media: mediaOf(imageMessage("the view"))}
	quoted := buildReply("wow", photo, pnAda.String()).GetExtendedTextMessage().GetContextInfo().GetQuotedMessage()
	if quoted.GetImageMessage().GetCaption() != "the view" {
		t.Errorf("photo quote = %v", quoted)
	}
}

// imageMessage is a received photo with a caption.
func imageMessage(caption string) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String(caption), Mimetype: proto.String("image/jpeg"),
		DirectPath: proto.String("/v/x"), MediaKey: []byte("k"),
	}}
}

func TestAlbumPhotosAreImages(t *testing.T) {
	evt := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: pnAda, Sender: pnAda}, ID: "A1"},
		Message: &waE2E.Message{AssociatedChildMessage: &waE2E.FutureProofMessage{Message: imageMessage("beach")}},
	}
	m, ok := messageFromEvent(evt, stubResolver)
	if !ok || m.Kind != KindImage || m.Text != "beach" || m.Media == nil {
		t.Errorf("album photo = kind %q text %q media %v", m.Kind, m.Text, m.Media != nil)
	}
}

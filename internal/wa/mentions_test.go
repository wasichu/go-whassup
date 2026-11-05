package wa

import (
	"slices"
	"testing"
)

func TestMentionWireMessages(t *testing.T) {
	ids := []string{"15551234567@s.whatsapp.net", "123456789012345@lid"}
	text := "Hi @15551234567 and @123456789012345"
	ordinary := buildText("hello", nil)
	if ordinary.GetConversation() != "hello" || ordinary.GetExtendedTextMessage() != nil {
		t.Fatal("ordinary text changed")
	}
	msg := buildText(text, ids)
	if msg.GetExtendedTextMessage().GetText() != text || !slices.Equal(contextInfo(msg).GetMentionedJID(), ids) {
		t.Fatalf("missing mention context: %v", msg)
	}
	reply := buildReply(text, Message{ID: "quoted", Sender: ids[0], Text: "original"}, ids[0], ids...)
	ctx := contextInfo(reply)
	if !slices.Equal(ctx.GetMentionedJID(), ids) || ctx.GetStanzaID() != "quoted" || ctx.GetQuotedMessage().GetConversation() != "original" {
		t.Fatalf("reply lost mention or quote: %v", reply)
	}
	ids[0] = "changed"
	if contextInfo(msg).GetMentionedJID()[0] == "changed" || ctx.GetMentionedJID()[0] == "changed" {
		t.Fatal("wire metadata aliases mutable input")
	}
}

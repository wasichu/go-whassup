package wa

import (
	"slices"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// buildText keeps ordinary messages compact; explicit mentions require extended
// text and ContextInfo so recipients receive actual WhatsApp mentions.
func buildText(text string, mentions []string) *waE2E.Message {
	if len(mentions) == 0 {
		return &waE2E.Message{Conversation: proto.String(text)}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String(text), ContextInfo: &waE2E.ContextInfo{MentionedJID: slices.Clone(mentions)},
	}}
}

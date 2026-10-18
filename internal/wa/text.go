package wa

import (
	"errors"
	"strings"
	"unicode"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ErrInvalidPhone is returned for phone numbers that can't be used for pairing.
var ErrInvalidPhone = errors.New("phone number must be in international format, e.g. +1 555 123 4567")

// NormalizePhone strips formatting from a phone number and returns the bare
// digits WhatsApp expects (country code included, no leading +).
func NormalizePhone(phone string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(phone) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && b.Len() == 0, r == ' ', r == '-', r == '.', r == '(', r == ')':
			// formatting only
		default:
			return "", ErrInvalidPhone
		}
	}
	digits := b.String()
	// E.164 allows at most 15 digits; anything under 8 can't include a country code.
	if len(digits) < 8 || len(digits) > 15 || digits[0] == '0' {
		return "", ErrInvalidPhone
	}
	return digits, nil
}

// Message kinds with special handling. Other kinds are derived from the
// protobuf field name (see firstSetField), e.g. "sticker" or "poll creation".
const (
	KindText     = "text"
	KindImage    = "image"
	KindVideo    = "video"
	KindDocument = "document"
	KindReaction = "reaction"
)

// messageContent extracts a message's kind and text (body, caption, file
// name or reaction emoji). ok is false for messages that carry no
// user-visible content (protocol messages such as revokes and key
// distribution).
func messageContent(msg *waE2E.Message) (kind, text string, ok bool) {
	if msg == nil {
		return "", "", false
	}
	switch {
	case msg.Conversation != nil:
		return KindText, msg.GetConversation(), true
	case msg.ExtendedTextMessage != nil:
		return KindText, msg.GetExtendedTextMessage().GetText(), true
	case msg.ImageMessage != nil:
		return KindImage, msg.GetImageMessage().GetCaption(), true
	case msg.VideoMessage != nil:
		return KindVideo, msg.GetVideoMessage().GetCaption(), true
	case msg.DocumentMessage != nil:
		doc := msg.GetDocumentMessage()
		return KindDocument, strings.TrimSpace(doc.GetFileName() + " " + doc.GetCaption()), true
	case msg.ReactionMessage != nil:
		return KindReaction, msg.GetReactionMessage().GetText(), true
	case msg.ProtocolMessage != nil, msg.SenderKeyDistributionMessage != nil:
		return "", "", false
	}
	if kind := firstSetField(msg); kind != "" {
		return kind, "", true
	}
	return "", "", false
}

// firstSetField names the populated field of msg in a readable form, e.g.
// "stickerMessage" -> "sticker", "pollCreationMessageV3" -> "poll creation".
func firstSetField(msg *waE2E.Message) string {
	var name string
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.Message() == nil { // skip scalars like messageContextInfo flags
			return true
		}
		if fd.JSONName() == "messageContextInfo" {
			return true
		}
		name = fd.JSONName()
		return false
	})
	return humanizeKind(name)
}

func humanizeKind(field string) string {
	field = strings.TrimRightFunc(field, unicode.IsDigit)
	field = strings.TrimSuffix(field, "V")
	field = strings.TrimSuffix(field, "Message")
	var b strings.Builder
	for i, r := range field {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteRune(' ')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

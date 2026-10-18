package wa

import (
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "+1 (555) 123-4567", want: "15551234567"},
		{in: "  447911123456 ", want: "447911123456"},
		{in: "+49.30.1234567", want: "49301234567"},
		{in: "0555123456", wantErr: true},       // national format, no country code
		{in: "12345", wantErr: true},            // too short
		{in: "1234567890123456", wantErr: true}, // too long for E.164
		{in: "1555abc4567", wantErr: true},
		{in: "1555+1234567", wantErr: true}, // + only allowed first
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizePhone(tt.in)
		if tt.wantErr {
			if !errors.Is(err, ErrInvalidPhone) {
				t.Errorf("NormalizePhone(%q) error = %v, want ErrInvalidPhone", tt.in, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestMessageText(t *testing.T) {
	tests := []struct {
		name   string
		msg    *waE2E.Message
		want   string
		wantOK bool
	}{
		{"nil", nil, "", false},
		{"conversation", &waE2E.Message{Conversation: proto.String("hi")}, "hi", true},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link https://x")}}, "link https://x", true},
		{"image no caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, "[image]", true},
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("cat")}}, "[image] cat", true},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}}, "[document] a.pdf", true},
		{"reaction", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}, "[reaction] 👍", true},
		{"protocol", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}, "", false},
		{"sticker via reflection", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, "[sticker]", true},
		{"poll v3 via reflection", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{}}, "[poll creation]", true},
		{"empty", &waE2E.Message{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := messageText(tt.msg)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("messageText() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

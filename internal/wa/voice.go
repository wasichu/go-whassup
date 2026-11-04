package wa

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func voiceMessage(data []byte, seconds uint32, waveform []byte, upload func([]byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)) (*waE2E.Message, error) {
	if len(data) > MaxSendSize || !bytes.HasPrefix(data, []byte("OggS")) || !bytes.Contains(data[:min(len(data), 64)], []byte("OpusHead")) || seconds == 0 {
		return nil, errors.New("voice note must be a nonempty Ogg Opus recording within the media size limit")
	}
	up, err := upload(data, whatsmeow.MediaAudio)
	if err != nil {
		return nil, fmt.Errorf("uploading voice note: %w", err)
	}
	return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(seconds),
		Waveform: bytes.Clone(waveform), URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
		FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
	}}, nil
}

// SendVoice sends through the same event/recorder path as other outgoing media.
func (w *WhatsApp) SendVoice(ctx context.Context, chat string, data []byte, seconds uint32, waveform []byte) (string, error) {
	cli := w.client()
	if cli.Store.ID == nil {
		return "", ErrNotPaired
	}
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("bad chat %q: %w", chat, err)
	}
	msg, err := voiceMessage(data, seconds, waveform, func(b []byte, t whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return cli.Upload(ctx, b, t)
	})
	if err != nil {
		return "", err
	}
	resp, err := cli.SendMessage(ctx, to, msg)
	if err != nil {
		return "", fmt.Errorf("sending voice note: %w", err)
	}
	w.emit(Message{ID: resp.ID, Chat: chat, Sender: cli.Store.ID.ToNonAD().String(), PushName: cli.Store.PushName,
		Timestamp: resp.Timestamp, FromMe: true, IsGroup: to.Server == types.GroupServer, Kind: "audio", Media: mediaOf(msg)})
	return resp.ID, nil
}

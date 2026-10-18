package wa

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Media describes a message's attachment well enough to download it later.
type Media struct {
	// Data is a serialized waE2E.Message with exactly one media field set
	// (image, video, audio, document or sticker), minus quoted context. It
	// holds the media key, so the store seals it like message text.
	Data     []byte
	Mime     string
	FileName string // documents only
	Size     int64
}

var (
	// ErrMediaExpired means WhatsApp no longer has the file (404/410).
	ErrMediaExpired = errors.New("media is no longer available on WhatsApp's servers")
	// ErrFileTooLarge is returned by SendFile above MaxSendSize.
	ErrFileTooLarge = errors.New("file is too large to send")
)

// MaxSendSize caps attachments. whatsmeow's Upload needs the whole file in
// memory, so keep this modest.
const MaxSendSize = 100 << 20

// mediaOf extracts msg's attachment, or nil if it has none.
func mediaOf(msg *waE2E.Message) *Media {
	var (
		wrapped        *waE2E.Message
		mimeType, name string
		size           uint64
	)
	switch {
	case msg.GetImageMessage() != nil:
		m := proto.Clone(msg.GetImageMessage()).(*waE2E.ImageMessage)
		m.ContextInfo, m.JPEGThumbnail = nil, nil
		wrapped, mimeType, size = &waE2E.Message{ImageMessage: m}, m.GetMimetype(), m.GetFileLength()
	case msg.GetVideoMessage() != nil:
		m := proto.Clone(msg.GetVideoMessage()).(*waE2E.VideoMessage)
		m.ContextInfo, m.JPEGThumbnail = nil, nil
		wrapped, mimeType, size = &waE2E.Message{VideoMessage: m}, m.GetMimetype(), m.GetFileLength()
	case msg.GetAudioMessage() != nil:
		m := proto.Clone(msg.GetAudioMessage()).(*waE2E.AudioMessage)
		m.ContextInfo = nil
		wrapped, mimeType, size = &waE2E.Message{AudioMessage: m}, m.GetMimetype(), m.GetFileLength()
	case msg.GetDocumentMessage() != nil:
		m := proto.Clone(msg.GetDocumentMessage()).(*waE2E.DocumentMessage)
		m.ContextInfo, m.JPEGThumbnail = nil, nil
		wrapped, mimeType, size, name = &waE2E.Message{DocumentMessage: m}, m.GetMimetype(), m.GetFileLength(), m.GetFileName()
	case msg.GetStickerMessage() != nil:
		m := proto.Clone(msg.GetStickerMessage()).(*waE2E.StickerMessage)
		m.ContextInfo, m.PngThumbnail = nil, nil
		wrapped, mimeType, size = &waE2E.Message{StickerMessage: m}, m.GetMimetype(), m.GetFileLength()
	default:
		return nil
	}
	data, err := proto.Marshal(wrapped)
	if err != nil {
		return nil // can't happen for a message we just unmarshalled
	}
	return &Media{Data: data, Mime: mimeType, FileName: name, Size: int64(size)}
}

// ParseMedia rebuilds a Media from its Data (as stored), recovering the
// MIME type, file name and size so only the sealed blob needs persisting.
func ParseMedia(data []byte) (*Media, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decoding media: %w", err)
	}
	m := mediaOf(&msg)
	if m == nil {
		return nil, errors.New("no attachment in media data")
	}
	return m, nil
}

// downloadable recovers the typed attachment whatsmeow needs to download.
func downloadable(m Media) (whatsmeow.DownloadableMessage, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(m.Data, &msg); err != nil {
		return nil, fmt.Errorf("decoding media: %w", err)
	}
	switch {
	case msg.ImageMessage != nil:
		return msg.ImageMessage, nil
	case msg.VideoMessage != nil:
		return msg.VideoMessage, nil
	case msg.AudioMessage != nil:
		return msg.AudioMessage, nil
	case msg.DocumentMessage != nil:
		return msg.DocumentMessage, nil
	case msg.StickerMessage != nil:
		return msg.StickerMessage, nil
	}
	return nil, errors.New("no attachment in media data")
}

// Download writes m's attachment to dst (streamed, so large files are fine).
func (w *WhatsApp) Download(ctx context.Context, m Media, dst *os.File) error {
	d, err := downloadable(m)
	if err != nil {
		return err
	}
	err = w.client().DownloadToFile(ctx, d, dst)
	switch {
	case errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404), errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410):
		return ErrMediaExpired
	case err != nil:
		return fmt.Errorf("downloading media: %w", err)
	}
	return nil
}

// imageMimes are sent as images; everything else goes as a document, which
// needs no type-specific metadata (video/audio need duration, waveform...).
var imageMimes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// buildFileMessage uploads data via upload and returns the message to send.
func buildFileMessage(data []byte, name, caption string, upload func([]byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)) (*waE2E.Message, error) {
	sniffed := http.DetectContentType(data)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = sniffed[:i]
	}
	if imageMimes[sniffed] {
		up, err := upload(data, whatsmeow.MediaImage)
		if err != nil {
			return nil, fmt.Errorf("uploading image: %w", err)
		}
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: optional(caption), Mimetype: proto.String(sniffed),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}}, nil
	}
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = sniffed
	}
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = mimeType[:i]
	}
	up, err := upload(data, whatsmeow.MediaDocument)
	if err != nil {
		return nil, fmt.Errorf("uploading document: %w", err)
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		Caption: optional(caption), Mimetype: proto.String(mimeType), FileName: proto.String(name),
		Title: proto.String(name),
		URL:   &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
		FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
	}}, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// SendFile sends the file at path to chat, as an image (JPEG/PNG/WebP) or
// otherwise as a document, and returns the new message's ID. Like SendText,
// the sent message is emitted as a Message event once acknowledged.
func (w *WhatsApp) SendFile(ctx context.Context, chat, path, caption string) (string, error) {
	cli := w.client()
	if cli.Store.ID == nil {
		return "", ErrNotPaired
	}
	to, err := types.ParseJID(chat)
	if err != nil {
		return "", fmt.Errorf("bad chat %q: %w", chat, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > MaxSendSize {
		return "", fmt.Errorf("%w: %d MB (max %d MB)", ErrFileTooLarge, info.Size()>>20, MaxSendSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	msg, err := buildFileMessage(data, filepath.Base(path), caption, func(b []byte, t whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return cli.Upload(ctx, b, t)
	})
	if err != nil {
		return "", err
	}
	resp, err := cli.SendMessage(ctx, to, msg)
	if err != nil {
		return "", fmt.Errorf("sending file: %w", err)
	}
	kind, text, _ := messageContent(msg)
	w.emit(Message{
		ID: resp.ID, Chat: chat, Sender: cli.Store.ID.ToNonAD().String(), PushName: cli.Store.PushName,
		Timestamp: resp.Timestamp, FromMe: true, IsGroup: to.Server == types.GroupServer,
		Kind: kind, Text: text, Media: mediaOf(msg),
	})
	return resp.ID, nil
}

package wa

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestMediaOfStripsQuotedContextAndKeepsType(t *testing.T) {
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String("cat"), Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(1234),
		DirectPath: proto.String("/v/t62/abc"), MediaKey: []byte("key"),
		JPEGThumbnail: []byte("thumb"),
		ContextInfo:   &waE2E.ContextInfo{QuotedMessage: &waE2E.Message{Conversation: proto.String("someone else's text")}},
	}}
	m := mediaOf(msg)
	if m == nil || m.Mime != "image/jpeg" || m.Size != 1234 {
		t.Fatalf("media = %+v", m)
	}
	if bytes.Contains(m.Data, []byte("someone else's text")) || bytes.Contains(m.Data, []byte("thumb")) {
		t.Error("quoted context or thumbnail kept in stored media")
	}
	d, err := downloadable(*m)
	if err != nil {
		t.Fatalf("downloadable: %v", err)
	}
	img, ok := d.(*waE2E.ImageMessage)
	if !ok || img.GetDirectPath() != "/v/t62/abc" || string(img.GetMediaKey()) != "key" {
		t.Errorf("round trip lost the typed attachment: %#v", d)
	}
	if msg.GetImageMessage().GetContextInfo() == nil {
		t.Error("mediaOf must not modify the original message")
	}
	if mediaOf(&waE2E.Message{Conversation: proto.String("text")}) != nil {
		t.Error("text message has no media")
	}
}

func TestDocumentMediaKeepsFileName(t *testing.T) {
	m := mediaOf(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf"), Mimetype: proto.String("application/pdf")}})
	if m == nil || m.FileName != "a.pdf" {
		t.Fatalf("media = %+v", m)
	}
	if _, err := downloadable(Media{Data: []byte("not a proto \xff")}); err == nil {
		t.Error("garbage media data accepted")
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBuildFileMessageImageOrDocument(t *testing.T) {
	var uploaded []whatsmeow.MediaType
	upload := func(_ []byte, mt whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		uploaded = append(uploaded, mt)
		return whatsmeow.UploadResponse{URL: "u", DirectPath: "/d", FileLength: 9}, nil
	}

	img, err := buildFileMessage(pngBytes(t), "photo.png", "look", upload)
	if err != nil || img.GetImageMessage() == nil || img.GetImageMessage().GetMimetype() != "image/png" || img.GetImageMessage().GetCaption() != "look" {
		t.Fatalf("png = %v, %v", img, err)
	}
	for name, data := range map[string][]byte{
		"notes.txt":  []byte("plain text"),
		"report.pdf": []byte("%PDF-1.7 ..."),
		"photo.heic": []byte("\x00\x00\x00\x18ftypheic"), // not sent as an image
	} {
		doc, err := buildFileMessage(data, name, "", upload)
		if err != nil || doc.GetDocumentMessage() == nil || doc.GetDocumentMessage().GetFileName() != name {
			t.Errorf("%s = %v, %v; want a document", name, doc, err)
		}
		if doc.GetDocumentMessage().Caption != nil {
			t.Errorf("%s: empty caption should be unset", name)
		}
	}
	if got := buildDocMime(t, upload, "report.pdf"); got != "application/pdf" {
		t.Errorf("pdf mime = %q", got)
	}
	if uploaded[0] != whatsmeow.MediaImage || uploaded[1] != whatsmeow.MediaDocument {
		t.Errorf("upload types = %v", uploaded)
	}

	failing := func([]byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return whatsmeow.UploadResponse{}, errors.New("network down")
	}
	if _, err := buildFileMessage([]byte("x"), "x.txt", "", failing); err == nil {
		t.Error("upload failure not reported")
	}
}

func buildDocMime(t *testing.T, upload func([]byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error), name string) string {
	t.Helper()
	m, err := buildFileMessage([]byte("%PDF-1.7"), name, "", upload)
	if err != nil {
		t.Fatal(err)
	}
	return m.GetDocumentMessage().GetMimetype()
}

func TestMessageFromEventCarriesMedia(t *testing.T) {
	for kind, msg := range map[string]*waE2E.Message{
		KindImage:    {ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
		KindVideo:    {VideoMessage: &waE2E.VideoMessage{}},
		KindDocument: {DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("x")}},
		"sticker":    {StickerMessage: &waE2E.StickerMessage{}},
		"audio":      {AudioMessage: &waE2E.AudioMessage{}},
	} {
		got, ok := messageFromEvent(textEventWith(msg), stubResolver)
		if !ok || got.Kind != kind || got.Media == nil {
			t.Errorf("%s: kind=%q media=%v ok=%v", kind, got.Kind, got.Media, ok)
		}
	}
	if got, _ := messageFromEvent(textEvent(typesSourceAda(), "hi"), stubResolver); got.Media != nil {
		t.Error("text message got media")
	}
}

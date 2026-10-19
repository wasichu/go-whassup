// Package watest provides a fake wa.Client: for tests, and for the demo
// (go-whassup demo), which runs the real UI on made-up chats.
package watest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Client is a scriptable wa.Client. Configure the exported fields before use.
type Client struct {
	wa.Handlers // AddEventHandler and Emit

	Own string // OwnJID while paired

	Paired     bool
	PairCode   string
	PairErr    error
	ConnectErr error
	// Logout outcome: PhoneUnlinked is returned on success; LogoutErr fails it.
	PhoneUnlinked bool
	LogoutErr     error
	// SendErr fails SendText; otherwise sent messages are emitted and recorded.
	SendErr error
	Sent    []wa.Message
	// LIDs maps privacy IDs to phone-number keys, for PhoneNumbers.
	LIDs map[string]string
	// Reactions sent with SendReaction (replies land in Sent, with ReplyTo).
	ReactionsSent []wa.Reaction
	// Files sent with SendFile; DownloadData is what Download writes (or
	// DownloadErr fails it).
	Files        []SentFile
	DownloadData []byte
	DownloadErr  error
	// DownloadFor, if set, chooses the downloaded content per attachment
	// (overrides DownloadData).
	DownloadFor func(wa.Media) []byte
	// Now stamps sent messages; nil uses fixed dates (stable for tests).
	Now       func() time.Time
	downloads atomic.Int32
	// MarkRead calls with a mode other than ReadLocal.
	Reads []ReadCall
	// Events emitted synchronously, in order, from a successful Pair,
	// PairQR or Connect.
	PairEvents    []wa.Event
	QREvents      []wa.Event
	ConnectEvents []wa.Event

	mu        sync.Mutex
	pairPhone string
	cancelled int
	connected bool
	closed    bool
}

var _ wa.Client = (*Client)(nil)

func (c *Client) Pair(_ context.Context, phone string) (string, error) {
	if c.Paired {
		return "", wa.ErrAlreadyPaired
	}
	c.mu.Lock()
	c.pairPhone = phone
	c.mu.Unlock()
	if c.PairErr != nil {
		return "", c.PairErr
	}
	for _, evt := range c.PairEvents {
		c.Emit(evt)
	}
	return c.PairCode, nil
}

func (c *Client) PairQR(context.Context) error {
	if c.Paired {
		return wa.ErrAlreadyPaired
	}
	if c.PairErr != nil {
		return c.PairErr
	}
	for _, evt := range c.QREvents {
		c.Emit(evt)
	}
	return nil
}

func (c *Client) CancelPairing() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled++
}

// Cancelled reports how many times CancelPairing was called.
func (c *Client) Cancelled() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelled
}

func (c *Client) Connect(context.Context) error {
	if !c.Paired {
		return wa.ErrNotPaired
	}
	if c.ConnectErr != nil {
		return c.ConnectErr
	}
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	for _, evt := range c.ConnectEvents {
		c.Emit(evt)
	}
	return nil
}

func (c *Client) Logout(context.Context) (bool, error) {
	if !c.Paired {
		return false, wa.ErrNotPaired
	}
	if c.LogoutErr != nil {
		return false, c.LogoutErr
	}
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	c.Paired = false
	return c.PhoneUnlinked, nil
}

// SendText emits the sent message like the real client. Timestamps are
// fixed so tests are deterministic.
func (c *Client) SendText(_ context.Context, chat, text string) error {
	if !c.Paired {
		return wa.ErrNotPaired
	}
	if c.SendErr != nil {
		return c.SendErr
	}
	c.mu.Lock()
	m := wa.Message{
		ID:        fmt.Sprintf("SENT%d", len(c.Sent)+1),
		Chat:      chat,
		Sender:    "me@s.whatsapp.net",
		Timestamp: c.stamp(time.Date(2026, 1, 1, 0, 0, len(c.Sent), 0, time.UTC)),
		FromMe:    true,
		Kind:      wa.KindText,
		Text:      text,
	}
	c.Sent = append(c.Sent, m)
	c.mu.Unlock()
	c.Emit(m)
	return nil
}

// SentFile records one SendFile call.
type SentFile struct{ Chat, Path, Caption string }

// SendFile records the call and emits a document message, like the real
// client emits what it sent.
func (c *Client) SendFile(_ context.Context, chat, path, caption string) (string, error) {
	if !c.Paired {
		return "", wa.ErrNotPaired
	}
	if c.SendErr != nil {
		return "", c.SendErr
	}
	c.mu.Lock()
	c.Files = append(c.Files, SentFile{Chat: chat, Path: path, Caption: caption})
	id := fmt.Sprintf("FILE%d", len(c.Files))
	c.mu.Unlock()
	c.Emit(wa.Message{
		ID: id, Chat: chat, Sender: "me@s.whatsapp.net", FromMe: true,
		Timestamp: c.stamp(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)), Kind: wa.KindDocument,
		Text:  filepath.Base(path),
		Media: DocumentMedia(filepath.Base(path)),
	})
	return id, nil
}

// SentFiles returns what SendFile sent.
func (c *Client) SentFiles() []SentFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.Files)
}

// Download writes DownloadData to dst, or fails with DownloadErr.
func (c *Client) Download(_ context.Context, m wa.Media, dst *os.File) error {
	c.downloads.Add(1)
	if c.DownloadErr != nil {
		return c.DownloadErr
	}
	data := c.DownloadData
	if c.DownloadFor != nil {
		data = c.DownloadFor(m)
	}
	_, err := dst.Write(data)
	return err
}

// Downloads reports how many downloads were attempted.
func (c *Client) Downloads() int { return int(c.downloads.Load()) }

// ReadCall records one MarkRead call that would send something.
type ReadCall struct {
	Chat string
	IDs  []string
	Mode wa.ReadMode
}

func (c *Client) MarkRead(_ context.Context, chat string, msgs []wa.Message, mode wa.ReadMode) error {
	if mode == wa.ReadLocal || len(msgs) == 0 {
		return nil
	}
	call := ReadCall{Chat: chat, Mode: mode}
	for _, m := range msgs {
		call.IDs = append(call.IDs, m.ID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Reads = append(c.Reads, call)
	return nil
}

// ReadCalls returns MarkRead calls that would have sent receipts.
func (c *Client) ReadCalls() []ReadCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.Reads)
}

// SentMessages returns what SendText sent, safely for concurrent use.
func (c *Client) SentMessages() []wa.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.Sent)
}

// OwnJID returns Own while paired.
func (c *Client) OwnJID() string {
	if !c.Paired {
		return ""
	}
	return c.Own
}

func (c *Client) IsPaired() bool { return c.Paired }

func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected, c.closed = false, true
	return nil
}

// PairPhone returns the phone number passed to Pair.
func (c *Client) PairPhone() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pairPhone
}

// Connected reports whether Connect succeeded and no Disconnect/Close followed.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Closed reports whether Close was called.
func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// DocumentMedia returns valid media for a document named name, as the real
// client would attach to a message.
func DocumentMedia(name string) *wa.Media { return SizedDocumentMedia(name, 0) }

// SizedDocumentMedia is DocumentMedia with a file size.
func SizedDocumentMedia(name string, size uint64) *wa.Media {
	data, err := proto.Marshal(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		FileName: proto.String(name), Mimetype: proto.String("application/octet-stream"),
		DirectPath: proto.String("/fake"), MediaKey: []byte("fake-key"), FileLength: proto.Uint64(size),
	}})
	if err != nil {
		panic(err) // a fixed, valid message always marshals
	}
	m, err := wa.ParseMedia(data)
	if err != nil {
		panic(err)
	}
	return m
}

// PhotoMedia is a photo attachment carrying WhatsApp's embedded JPEG
// thumbnail (thumb), as received.
func PhotoMedia(thumb []byte) *wa.Media {
	data, err := proto.Marshal(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype: proto.String("image/jpeg"), DirectPath: proto.String("/fake"), MediaKey: []byte("fake-key"),
		FileLength: proto.Uint64(123456), JPEGThumbnail: thumb,
	}})
	if err != nil {
		panic(err) // a fixed, valid message always marshals
	}
	m, err := wa.ParseMedia(data)
	if err != nil {
		panic(err)
	}
	return m
}

// stamp is Now(), or fixed (the test default) when Now isn't set.
func (c *Client) stamp(fixed time.Time) time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return fixed
}

// SendReply sends like SendText, as a reply to to.
func (c *Client) SendReply(_ context.Context, chat, text string, to wa.Message) error {
	if !c.Paired {
		return wa.ErrNotPaired
	}
	if c.SendErr != nil {
		return c.SendErr
	}
	c.mu.Lock()
	m := wa.Message{
		ID: fmt.Sprintf("SENT%d", len(c.Sent)+1), Chat: chat, Sender: "me@s.whatsapp.net",
		Timestamp: c.stamp(time.Date(2026, 1, 1, 0, 0, len(c.Sent), 0, time.UTC)),
		FromMe:    true, Kind: wa.KindText, Text: text,
		ReplyTo: &wa.Reply{ID: to.ID, Sender: to.Sender},
	}
	c.Sent = append(c.Sent, m)
	c.mu.Unlock()
	c.Emit(m)
	return nil
}

// SendReaction records the reaction and emits it, as the real client does.
func (c *Client) SendReaction(_ context.Context, chat string, to wa.Message, emoji string) error {
	if !c.Paired {
		return wa.ErrNotPaired
	}
	if c.SendErr != nil {
		return c.SendErr
	}
	r := wa.Reaction{
		Chat: chat, MessageID: to.ID, Sender: "me@s.whatsapp.net", FromMe: true, Emoji: emoji,
		At: c.stamp(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)),
	}
	c.mu.Lock()
	c.ReactionsSent = append(c.ReactionsSent, r)
	c.mu.Unlock()
	c.Emit(r)
	return nil
}

// SentReactions returns the reactions sent so far.
func (c *Client) SentReactions() []wa.Reaction {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ReactionsSent)
}

// PhoneNumbers returns what LIDs knows about lids.
func (c *Client) PhoneNumbers(_ context.Context, lids []string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for _, l := range lids {
		if pn, ok := c.LIDs[l]; ok {
			out[l] = pn
		}
	}
	return out, nil
}

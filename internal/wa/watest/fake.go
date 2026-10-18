// Package watest provides a fake wa.Client for tests.
package watest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

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
	// Files sent with SendFile; DownloadData is what Download writes (or
	// DownloadErr fails it).
	Files        []SentFile
	DownloadData []byte
	DownloadErr  error
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
		Timestamp: time.Date(2026, 1, 1, 0, 0, len(c.Sent), 0, time.UTC),
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
		Timestamp: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Kind: wa.KindDocument,
		Text:  filepath.Base(path),
		Media: &wa.Media{Data: []byte("fake"), Mime: "application/octet-stream", FileName: filepath.Base(path)},
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
func (c *Client) Download(_ context.Context, _ wa.Media, dst *os.File) error {
	if c.DownloadErr != nil {
		return c.DownloadErr
	}
	_, err := dst.Write(c.DownloadData)
	return err
}

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

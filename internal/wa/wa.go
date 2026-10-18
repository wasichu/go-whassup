package wa

import (
	"context"
	"errors"
	"time"
)

// ErrNotPaired is returned by Connect when the device has never been linked to
// a phone. Call Pair first.
var ErrNotPaired = errors.New("device is not paired")

// ErrAlreadyPaired is returned by Pair when a session already exists.
var ErrAlreadyPaired = errors.New("device is already paired")

// Client is the protocol surface the rest of the app depends on. The UI and
// cmd packages use this interface so they can be exercised with a fake.
type Client interface {
	// AddEventHandler registers fn to receive every Event and returns a
	// function that removes it. Handlers run in registration order on
	// background goroutines: they must not touch UI state (use Subscribe) and
	// a slow handler delays the rest. Register before calling Pair or Connect.
	AddEventHandler(fn func(Event)) (remove func())

	// Pair links this device using a phone-number pairing code. It connects,
	// requests a code for phone (international format, digits only after
	// normalization) and returns it for the user to enter on their phone.
	// The outcome arrives later as PairSuccess (then Connected) or PairFailed.
	// ctx bounds the pairing window and the resulting connection: cancelling
	// it aborts pairing and stops reconnects.
	Pair(ctx context.Context, phone string) (code string, err error)

	// Connect connects an already-paired device. It returns ErrNotPaired if
	// the device has never been linked. ctx bounds the connection's lifetime
	// (including automatic reconnects), not just the dial.
	Connect(ctx context.Context) error

	// IsPaired reports whether a session exists in the store.
	IsPaired() bool

	// Disconnect closes the connection. It does not emit Disconnected.
	Disconnect()

	// Close disconnects and releases the session store.
	Close() error
}

// Event is any value delivered to handlers registered with AddEventHandler.
type Event interface{ isEvent() }

// Connected is emitted once the client is authenticated and online.
type Connected struct{}

// Disconnected is emitted when the server or network drops the connection.
// whatsmeow reconnects automatically.
type Disconnected struct{}

// LoggedOut is emitted when the phone unlinks this device. The session is
// gone and the user must pair again.
type LoggedOut struct{ Reason string }

// PairSuccess is emitted when the phone accepts the pairing code. A
// reconnect follows; wait for Connected before sending anything.
type PairSuccess struct{ JID string }

// PairFailed is emitted when pairing times out or is rejected.
type PairFailed struct{ Reason string }

// Message is a chat message received live, echoed from another of the
// user's devices, or delivered by history sync.
type Message struct {
	ID        string
	Chat      string // stable chat key, see ChatKey
	Sender    string // stable user key, see ChatKey
	PushName  string
	Timestamp time.Time
	FromMe    bool
	IsGroup   bool
	Kind      string // "text", "image", "reaction", ... see messageKind
	Text      string // body or caption; may be empty for media
}

// Summary renders the message for one-line display, e.g. "[image] caption".
func (m Message) Summary() string {
	if m.Kind == KindText {
		return m.Text
	}
	if m.Text == "" {
		return "[" + m.Kind + "]"
	}
	return "[" + m.Kind + "] " + m.Text
}

// Chat describes a conversation, as delivered by history sync.
type Chat struct {
	JID     string // stable chat key
	Name    string // group subject or contact name; may be empty
	IsGroup bool
}

// HistorySync is a batch of past conversations pushed by the phone after
// linking (and occasionally later). Messages within a batch are not ordered.
type HistorySync struct {
	Chats     []Chat
	Messages  []Message
	PushNames map[string]string // user key -> push name
}

func (Connected) isEvent()    {}
func (Disconnected) isEvent() {}
func (LoggedOut) isEvent()    {}
func (PairSuccess) isEvent()  {}
func (PairFailed) isEvent()   {}
func (Message) isEvent()      {}
func (HistorySync) isEvent()  {}

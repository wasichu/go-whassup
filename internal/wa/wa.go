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

	// PairQR links this device by QR code: it connects and emits a
	// PairQRCode event for each code to display (they rotate every ~20s).
	// The outcome arrives as PairSuccess (then Connected) or PairFailed.
	// ctx bounds the resulting connection, as for Pair.
	PairQR(ctx context.Context) error

	// CancelPairing stops an in-progress Pair or PairQR without emitting
	// PairFailed. It is a no-op once pairing has succeeded.
	CancelPairing()

	// Connect connects an already-paired device. It returns ErrNotPaired if
	// the device has never been linked. ctx bounds the connection's lifetime
	// (including automatic reconnects), not just the dial.
	Connect(ctx context.Context) error

	// Logout unlinks this device and resets to a fresh, unpaired one so Pair
	// can run again. It removes the device from the phone's Linked devices
	// when it can reach WhatsApp, and always deletes the local session;
	// phoneUnlinked reports whether the first part succeeded. Returns
	// ErrNotPaired if there is nothing to log out.
	Logout(ctx context.Context) (phoneUnlinked bool, err error)

	// SendText sends a text message to chat (a chat key, e.g. from
	// Message.Chat). Once the server acknowledges it, the sent message is
	// emitted as a Message event with FromMe set, like any other message:
	// that event is the only way it reaches the store and the UI. Returns
	// ErrNotPaired before pairing, and an error while disconnected.
	SendText(ctx context.Context, chat, text string) error

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

// PairQRCode carries a QR code to display during PairQR. Each replaces the
// previous one.
type PairQRCode struct{ Code string }

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

	// SenderName is the best display name for Sender (address book, then
	// push name). Filled in by the store when reading; empty on the wire.
	SenderName string
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

// Contact is a person's names as known to WhatsApp. Empty fields are
// unknown, not cleared.
type Contact struct {
	JID          string // stable user key, see ChatKey
	Name         string // the user's own address-book name for them
	PushName     string // the name they chose for themselves
	BusinessName string // verified business name
}

// Contacts carries contact names: a full snapshot after connecting and
// after app-state sync, or single updates when a name changes. Consumers
// upsert; an entry never means "delete".
type Contacts struct {
	List     []Contact
	Snapshot bool
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
func (PairQRCode) isEvent()   {}
func (Message) isEvent()      {}
func (HistorySync) isEvent()  {}
func (Contacts) isEvent()     {}

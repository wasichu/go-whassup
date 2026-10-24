package wa

import (
	"context"
	"errors"
	"os"
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

	// PhoneNumbers maps privacy IDs ("…@lid" keys) to phone-number chat keys,
	// for those WhatsApp has told this device about since; the rest are
	// left out. It reads the local session, so it's cheap.
	PhoneNumbers(ctx context.Context, lids []string) (map[string]string, error)

	// SendReply sends text as a reply to message to, quoting it; it comes
	// back as a Message event (with ReplyTo) like SendText's.
	SendReply(ctx context.Context, chat, text string, to Message) error

	// SendReaction reacts to message to with emoji; "" removes your
	// reaction. It comes back as a Reaction event once sent.
	SendReaction(ctx context.Context, chat string, to Message, emoji string) error

	// SendEdit replaces the text of target, a text message you sent within
	// EditWindow. It comes back as a MessageEdited event once sent.
	SendEdit(ctx context.Context, chat string, target Message, text string) error

	// SendDelete deletes target, a message you sent within DeleteWindow,
	// for everyone. It comes back as a MessageDeleted event once sent.
	SendDelete(ctx context.Context, chat string, target Message) error

	// MarkRead reports msgs (incoming messages in chat) as read according
	// to mode. ReadLocal sends nothing and returns nil.
	MarkRead(ctx context.Context, chat string, msgs []Message, mode ReadMode) error

	// OwnJID is the linked account's chat key (e.g. for the chat with
	// yourself), or "" before pairing.
	OwnJID() string

	// SendFile sends a file (images as images, anything else as a
	// document) and returns its message ID; see WhatsApp.SendFile.
	SendFile(ctx context.Context, chat, path, caption string) (id string, err error)

	// Download writes a message's attachment to dst. ErrMediaExpired means
	// WhatsApp no longer has it.
	Download(ctx context.Context, m Media, dst *os.File) error

	// IsPaired reports whether a session exists in the store.
	IsPaired() bool

	// Disconnect closes the connection. It does not emit Disconnected.
	Disconnect()

	// Close disconnects and releases the session store.
	Close() error
}

// ReadMode says who learns that the user read messages.
type ReadMode string

const (
	ReadLocal    ReadMode = "local"    // nobody: nothing is sent
	ReadDevices  ReadMode = "devices"  // the user's other devices ("read-self")
	ReadEveryone ReadMode = "everyone" // also the senders (blue ticks)
)

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
	Media     *Media // attachment, if any
	ReplyTo   *Reply // the message this one answers, if it's a reply

	// Filled in by the store when reading; empty on the wire.
	SenderName string          // best display name for Sender (address book, then push name)
	Edited     bool            // the text was changed after sending
	Deleted    bool            // deleted for everyone; Text is empty
	Reactions  []ReactionCount // most popular first
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

// Reply identifies the message a reply answers. Only the pointer travels
// with a reply here: the quoted text is looked up in the store when shown.
type Reply struct {
	ID     string
	Sender string // chat key of the quoted message's author; "" if unknown
}

// Chat describes a conversation, as delivered by history sync.
type Chat struct {
	JID         string // stable chat key
	Name        string // group subject or contact name; may be empty
	IsGroup     bool
	UnreadCount int       // unread messages per the phone at sync time
	MutedUntil  time.Time // zero = not muted; MutedForever = until unmuted
}

// MessageEdited replaces the text of an earlier message.
type MessageEdited struct {
	Chat, ID string
	Kind     string
	Text     string
	At       time.Time
}

// MessageDeleted removes an earlier message: "delete for everyone" leaves a
// placeholder (ForMe false); "delete for me" removes it entirely.
type MessageDeleted struct {
	Chat, ID string
	At       time.Time
	ForMe    bool
}

// Reaction sets (or, with an empty Emoji, removes) Sender's reaction to a
// message.
type Reaction struct {
	Chat, MessageID string
	Sender          string
	FromMe          bool
	Emoji           string
	At              time.Time
}

// ReactionCount summarises one emoji's reactions to a message.
type ReactionCount struct {
	Emoji string
	Count int
	Mine  bool // the user is one of the reactors
}

// Group is a group chat's metadata.
type Group struct {
	JID   string
	Name  string
	Topic string
	// Communities: Community is the parent's JID for a group inside one;
	// IsCommunity marks the parent itself (it has no chat of its own), and
	// Announcements the community's announcement group, which WhatsApp
	// names after the community.
	Community     string
	IsCommunity   bool
	Announcements bool
	Participants  []Participant // nil means unknown (keep what's stored)
}

// Participant is a group member.
type Participant struct {
	JID   string // stable user key
	Admin bool
}

// Groups carries group metadata: a snapshot of all joined groups after
// connecting, or single groups when they change or are joined.
type Groups struct {
	List     []Group
	Snapshot bool
}

// ChatMuted reports a chat's mute setting, changed on another device (or
// synced after linking). Until is zero when unmuted.
type ChatMuted struct {
	Chat  string
	Until time.Time // MutedForever = until unmuted
}

// MutedForever is the Until of a chat muted with no end ("Always").
var MutedForever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// muteEnd interprets WhatsApp's mute end: negative means forever, and
// newer clients send milliseconds where older data has seconds (told
// apart by size: 1e12 seconds is tens of millennia away).
func muteEnd(v int64) time.Time {
	switch {
	case v < 0:
		return MutedForever
	case v == 0:
		return time.Time{}
	case v >= 1e12:
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// ChatRead reports that the user read a chat on another device, up to At.
type ChatRead struct {
	Chat string
	At   time.Time
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
	Reactions []Reaction        // attached to messages in the batch
	PushNames map[string]string // user key -> push name
}

func (Connected) isEvent()      {}
func (Disconnected) isEvent()   {}
func (LoggedOut) isEvent()      {}
func (PairSuccess) isEvent()    {}
func (PairFailed) isEvent()     {}
func (PairQRCode) isEvent()     {}
func (Message) isEvent()        {}
func (HistorySync) isEvent()    {}
func (Contacts) isEvent()       {}
func (ChatRead) isEvent()       {}
func (MessageEdited) isEvent()  {}
func (MessageDeleted) isEvent() {}
func (Reaction) isEvent()       {}
func (Groups) isEvent()         {}
func (ChatMuted) isEvent()      {}

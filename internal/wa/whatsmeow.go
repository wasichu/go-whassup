package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/wasichu/go-whassup/internal/sqlite"
)

const (
	// Shown in the phone's "Linked devices" list.
	deviceName = "go-whassup"
	// PairPhone validates this server-side: it must be "Browser (OS)" using
	// common names, so it can't be our own app name.
	pairDisplayName = "Chrome (Linux)"
)

// WhatsApp implements Client on top of whatsmeow.
type WhatsApp struct {
	Handlers // provides AddEventHandler

	db        *sql.DB
	container *sqlstore.Container
	log       *slog.Logger

	mu         sync.RWMutex
	cli        *whatsmeow.Client // replaced by Logout; read via client()
	opts       Options
	cancelPair context.CancelFunc // set while a pairing is in progress
}

var _ Client = (*WhatsApp)(nil)

// Options configures Open.
type Options struct {
	// RequestFullSync asks the phone for ~1 year of history instead of
	// ~3 months. Only sent when pairing.
	RequestFullSync bool
	// FullSyncDays limits the full sync; 0 = the phone's default. Only sent
	// with RequestFullSync.
	FullSyncDays int
}

// Open loads (or creates) the session store at dbPath and prepares a client.
// It does not connect.
func Open(ctx context.Context, dbPath string, log *slog.Logger, opts Options) (*WhatsApp, error) {
	db, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening session db: %w", err)
	}
	container := sqlstore.NewWithDB(db, sqlite.Driver, newLogger(log, "store"))
	if err := container.Upgrade(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("migrating session db: %w", err), db.Close())
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("loading device: %w", err), db.Close())
	}

	w := &WhatsApp{db: db, container: container, log: log, opts: opts}
	w.setDevice(device)
	return w, nil
}

// SetOptions replaces the pairing-time options; they take effect at the
// next Pair or PairQR.
func (w *WhatsApp) SetOptions(opts Options) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opts = opts
}

// applyDeviceProps writes our identity and history request into
// whatsmeow's process-wide DeviceProps, which are sent when pairing.
func (w *WhatsApp) applyDeviceProps() {
	w.mu.RLock()
	opts := w.opts
	w.mu.RUnlock()
	store.SetOSInfo(deviceName, [3]uint32{0, 1, 0})
	store.DeviceProps.RequireFullSync = proto.Bool(opts.RequestFullSync)
	store.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = nil
	if opts.RequestFullSync && opts.FullSyncDays > 0 {
		store.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = proto.Uint32(uint32(opts.FullSyncDays))
	}
}

// setDevice builds a whatsmeow client for device and makes it current.
func (w *WhatsApp) setDevice(device *store.Device) {
	cli := whatsmeow.NewClient(device, newLogger(w.log, "client"))
	cli.AddEventHandler(w.handleEvent)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cli = cli
}

func (w *WhatsApp) client() *whatsmeow.Client {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cli
}

// logoutConnectTimeout bounds how long Logout waits to come online to tell
// WhatsApp about the unlink before falling back to a local-only reset.
const logoutConnectTimeout = 15 * time.Second

// Logout unlinks this device and resets to a fresh, unpaired one. It first
// tries to unlink on WhatsApp's side (removing the entry from the phone's
// Linked devices); if that isn't possible (offline, or the phone already
// unlinked us) it deletes the local session anyway. phoneUnlinked reports
// whether the WhatsApp-side unlink succeeded. Stored messages are untouched.
func (w *WhatsApp) Logout(ctx context.Context) (phoneUnlinked bool, err error) {
	cli := w.client()
	if cli.Store.ID == nil {
		return false, ErrNotPaired
	}
	phoneUnlinked = w.remoteLogout(ctx, cli)
	if !phoneUnlinked {
		cli.Disconnect()
		if err := cli.Store.Delete(ctx); err != nil {
			return false, fmt.Errorf("deleting session: %w", err)
		}
	}
	w.setDevice(w.container.NewDevice())
	return phoneUnlinked, nil
}

// remoteLogout connects if needed and asks WhatsApp to unlink this device.
// On success whatsmeow has also disconnected and deleted the local session.
func (w *WhatsApp) remoteLogout(ctx context.Context, cli *whatsmeow.Client) bool {
	if !cli.IsConnected() {
		if err := cli.ConnectContext(ctx); err != nil {
			w.log.Warn("logout: could not connect; resetting locally", "err", err)
			return false
		}
	}
	if !cli.WaitForConnection(logoutConnectTimeout) {
		w.log.Warn("logout: not authenticated in time; resetting locally")
		return false
	}
	if err := cli.Logout(ctx); err != nil {
		w.log.Warn("logout: unlink request failed; resetting locally", "err", err)
		return false
	}
	return true
}

func (w *WhatsApp) OwnJID() string {
	if id := w.client().Store.ID; id != nil {
		return id.ToNonAD().String()
	}
	return ""
}

func (w *WhatsApp) IsPaired() bool { return w.client().Store.ID != nil }

func (w *WhatsApp) Connect(ctx context.Context) error {
	if !w.IsPaired() {
		return ErrNotPaired
	}
	if err := w.client().ConnectContext(ctx); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	return nil
}

func (w *WhatsApp) Pair(ctx context.Context, phone string) (string, error) {
	digits, err := NormalizePhone(phone)
	if err != nil {
		return "", err
	}
	qrCh, err := w.startPairing(ctx)
	if err != nil {
		return "", err
	}

	// PairPhone only works once the first QR item shows the login socket is up.
	select {
	case item, ok := <-qrCh:
		if !ok {
			return "", errors.New("pairing cancelled")
		}
		if item.Event != whatsmeow.QRChannelEventCode {
			w.CancelPairing()
			return "", fmt.Errorf("pairing not ready: %s", qrFailureReason(item))
		}
	case <-ctx.Done():
		w.CancelPairing()
		return "", ctx.Err()
	}

	code, err := w.client().PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, pairDisplayName)
	if err != nil {
		w.CancelPairing()
		return "", fmt.Errorf("requesting pairing code: %w", err)
	}
	go w.watchPairing(qrCh, false)
	return code, nil
}

func (w *WhatsApp) PairQR(ctx context.Context) error {
	qrCh, err := w.startPairing(ctx)
	if err != nil {
		return err
	}
	go w.watchPairing(qrCh, true)
	return nil
}

// startPairing opens the QR channel and connects. The channel gets its own
// cancellable context so CancelPairing can stop it the way whatsmeow
// intends (close channel, remove handler, disconnect), while ctx keeps
// bounding the connection a successful pairing leaves behind.
func (w *WhatsApp) startPairing(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	if w.IsPaired() {
		return nil, ErrAlreadyPaired
	}
	w.CancelPairing() // at most one pairing at a time
	// After a remote logout whatsmeow has deleted the device; its stores
	// are no-ops, so pair on a fresh one.
	if w.client().Store.Deleted {
		w.setDevice(w.container.NewDevice())
	}
	w.applyDeviceProps()

	pairCtx, cancel := context.WithCancel(ctx)
	cli := w.client()
	qrCh, err := cli.GetQRChannel(pairCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opening pairing channel: %w", err)
	}
	w.mu.Lock()
	w.cancelPair = cancel
	w.mu.Unlock()
	if err := cli.ConnectContext(ctx); err != nil {
		w.CancelPairing()
		return nil, fmt.Errorf("connecting for pairing: %w", err)
	}
	return qrCh, nil
}

// CancelPairing stops an in-progress Pair or PairQR. After a pairing has
// succeeded the channel is already closed, so this can't drop the
// resulting connection.
func (w *WhatsApp) CancelPairing() {
	w.mu.Lock()
	cancel := w.cancelPair
	w.cancelPair = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
		// If cancelled before the first QR event, whatsmeow's emitter never
		// started and won't disconnect for us.
		if w.client().Store.ID == nil {
			w.client().Disconnect()
		}
	}
}

// watchPairing drains the QR channel until pairing resolves. Success is
// reported by the PairSuccess event handler; this reports failures and,
// for QR pairing, each new code. A user cancel closes the channel without
// a terminal item, so it emits nothing.
func (w *WhatsApp) watchPairing(qrCh <-chan whatsmeow.QRChannelItem, forwardCodes bool) {
	for item := range qrCh {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			if forwardCodes {
				w.emit(PairQRCode{Code: compactQR(item.Code)})
			}
		case whatsmeow.QRChannelSuccess.Event:
		default:
			w.emit(PairFailed{Reason: qrFailureReason(item)})
		}
	}
}

// qrLinkPrefix is the deep link whatsmeow wraps QR payloads in so a phone's
// camera app can open WhatsApp. WhatsApp's own Linked devices scanner reads
// the bare "2@ref,keys..." form, which Baileys still sends, and dropping
// the prefix shrinks the code from 61 to 57 modules (65x33 -> 61x31 cells).
const qrLinkPrefix = "https://wa.me/settings/linked_devices#"

func compactQR(code string) string { return strings.TrimPrefix(code, qrLinkPrefix) }

func qrFailureReason(item whatsmeow.QRChannelItem) string {
	switch {
	case item.Event == whatsmeow.QRChannelTimeout.Event:
		return "timed out waiting for the code to be entered"
	case item.Event == whatsmeow.QRChannelClientOutdated.Event:
		return "client outdated: update whatsmeow"
	case item.Error != nil:
		return item.Error.Error()
	default:
		return item.Event
	}
}

func (w *WhatsApp) SendText(ctx context.Context, chat, text string) error {
	cli := w.client()
	if cli.Store.ID == nil {
		return ErrNotPaired
	}
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("bad chat %q: %w", chat, err)
	}
	resp, err := cli.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return fmt.Errorf("sending message: %w", err)
	}
	// whatsmeow doesn't echo messages this device sends; emit it ourselves
	// so the recorder and UI see it through the normal event path.
	w.emit(Message{
		ID:        resp.ID,
		Chat:      chat,
		Sender:    cli.Store.ID.ToNonAD().String(),
		PushName:  cli.Store.PushName,
		Timestamp: resp.Timestamp,
		FromMe:    true,
		IsGroup:   to.Server == types.GroupServer,
		Kind:      KindText,
		Text:      text,
	})
	return nil
}

// announceName tells the server our push name so recipients don't see "-"
// as the sender. It uses "unavailable" so this doesn't mark the user online
// or switch on active read receipts.
func (w *WhatsApp) announceName() {
	err := w.client().SendPresence(context.Background(), types.PresenceUnavailable)
	if err != nil {
		// Right after pairing the push name may not have synced yet; the
		// next connection will succeed.
		w.log.Debug("announcing push name", "err", err)
	}
}

// emitContactSnapshot emits every known contact. It reads whatsmeow's local
// store, so it is cheap and works whenever a session exists.
func (w *WhatsApp) emitContactSnapshot() {
	cli := w.client()
	if cli == nil || cli.Store.ID == nil {
		return
	}
	all, err := cli.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		w.log.Warn("reading contacts", "err", err)
		return
	}
	list := contactsFromStore(all, w.resolveKey)
	w.log.Info("contacts snapshot", "count", len(list))
	w.emit(Contacts{List: list, Snapshot: true})
}

func (w *WhatsApp) Disconnect() { w.client().Disconnect() }

func (w *WhatsApp) Close() error {
	w.CancelPairing()
	w.client().Disconnect()
	if err := w.db.Close(); err != nil {
		return fmt.Errorf("closing session db: %w", err)
	}
	return nil
}

func (w *WhatsApp) emit(e Event) { w.Emit(e) }

// resolveKey implements keyResolver using the message's alternate address
// first, then whatsmeow's LID->phone-number mapping store.
func (w *WhatsApp) resolveKey(jid, alt types.JID) string {
	jid = jid.ToNonAD()
	if jid.Server != types.HiddenUserServer {
		return jid.String()
	}
	if alt.Server == types.DefaultUserServer {
		return alt.ToNonAD().String()
	}
	// Local DB lookup on whatsmeow's event goroutine; no request context exists here.
	pn, err := w.client().Store.LIDs.GetPNForLID(context.Background(), jid)
	if err != nil {
		w.log.Debug("LID lookup failed", "lid", jid, "err", err)
	}
	if !pn.IsEmpty() {
		return pn.ToNonAD().String()
	}
	return jid.String()
}

// handleEvent runs on whatsmeow's goroutines. It translates protocol events
// into our plain Event values and must never touch UI state directly.
func (w *WhatsApp) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Connected:
		go w.announceName() // not on whatsmeow's event goroutine: it sends a node
		w.emit(Connected{})
		go w.emitContactSnapshot()
	case *events.AppStateSyncComplete:
		go w.emitContactSnapshot() // address-book names arrive via app state
	case *events.Contact:
		if v.FromFullSync {
			return // the AppStateSyncComplete snapshot covers these
		}
		name := v.Action.GetFullName()
		if name == "" {
			name = v.Action.GetFirstName()
		}
		var pn types.JID
		if p, err := types.ParseJID(v.Action.GetPnJID()); err == nil {
			pn = p
		}
		w.emit(Contacts{List: []Contact{{JID: w.resolveKey(v.JID, pn), Name: name}}})
	case *events.PushName:
		w.emit(Contacts{List: []Contact{{JID: w.resolveKey(v.JID, v.JIDAlt), PushName: v.NewPushName}}})
	case *events.BusinessName:
		w.emit(Contacts{List: []Contact{{JID: w.resolveKey(v.JID, types.EmptyJID), BusinessName: v.NewBusinessName}}})
	case *events.Disconnected:
		w.emit(Disconnected{})
	case *events.StreamReplaced:
		w.log.Warn("another client took over this session")
		w.emit(Disconnected{})
	case *events.LoggedOut:
		w.emit(LoggedOut{Reason: v.Reason.String()})
	case *events.TemporaryBan:
		w.log.Error("temporary ban", "detail", v.String())
		w.emit(Disconnected{})
	case *events.PairSuccess:
		w.emit(PairSuccess{JID: v.ID.ToNonAD().String()})
	case *events.Message:
		if msg, ok := messageFromEvent(v, w.resolveKey); ok {
			w.emit(msg)
		}
	case *events.HistorySync:
		if h, ok := historyFromProto(v.Data, w.client().ParseWebMessage, w.resolveKey, w.log); ok {
			w.log.Info("history sync", "type", v.Data.GetSyncType().String(),
				"chats", len(h.Chats), "messages", len(h.Messages), "push_names", len(h.PushNames))
			w.emit(h)
		}
	default:
		// App-state sync, receipts, presence etc. Handled in later steps; a
		// fresh pairing produces a flood of these.
		w.log.Debug("unhandled event", "type", fmt.Sprintf("%T", evt))
	}
}

package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
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

	mu  sync.RWMutex
	cli *whatsmeow.Client // replaced by Logout; read via client()
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
	// DeviceProps is whatsmeow's process-wide device description; it is sent
	// to the phone when pairing.
	store.SetOSInfo(deviceName, [3]uint32{0, 1, 0})
	store.DeviceProps.RequireFullSync = proto.Bool(opts.RequestFullSync)
	store.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = nil
	if opts.RequestFullSync && opts.FullSyncDays > 0 {
		store.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = proto.Uint32(uint32(opts.FullSyncDays))
	}

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

	w := &WhatsApp{db: db, container: container, log: log}
	w.setDevice(device)
	return w, nil
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
	if w.IsPaired() {
		return "", ErrAlreadyPaired
	}
	digits, err := NormalizePhone(phone)
	if err != nil {
		return "", err
	}

	// Order matters: the QR channel must exist before connecting, and
	// PairPhone only works once the first QR item shows the login socket is up.
	qrCh, err := w.client().GetQRChannel(ctx)
	if err != nil {
		return "", fmt.Errorf("opening pairing channel: %w", err)
	}
	if err := w.client().ConnectContext(ctx); err != nil {
		return "", fmt.Errorf("connecting for pairing: %w", err)
	}

	select {
	case item, ok := <-qrCh:
		if !ok {
			return "", errors.New("pairing channel closed before it was ready")
		}
		if item.Event != whatsmeow.QRChannelEventCode {
			w.client().Disconnect()
			return "", fmt.Errorf("pairing not ready: %s", qrFailureReason(item))
		}
	case <-ctx.Done():
		w.client().Disconnect()
		return "", ctx.Err()
	}

	code, err := w.client().PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, pairDisplayName)
	if err != nil {
		w.client().Disconnect()
		return "", fmt.Errorf("requesting pairing code: %w", err)
	}

	go w.watchPairing(qrCh)
	return code, nil
}

// watchPairing drains the QR channel until pairing resolves. Success is
// reported by the PairSuccess event handler; this only reports failures.
func (w *WhatsApp) watchPairing(qrCh <-chan whatsmeow.QRChannelItem) {
	for item := range qrCh {
		switch item.Event {
		case whatsmeow.QRChannelEventCode, whatsmeow.QRChannelSuccess.Event:
			// Rotating QR codes are irrelevant to phone-code pairing.
		default:
			w.emit(PairFailed{Reason: qrFailureReason(item)})
		}
	}
}

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

func (w *WhatsApp) Disconnect() { w.client().Disconnect() }

func (w *WhatsApp) Close() error {
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
		w.emit(Connected{})
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

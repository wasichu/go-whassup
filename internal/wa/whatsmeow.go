package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

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

	cli *whatsmeow.Client
	db  *sql.DB
	log *slog.Logger
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

	w := &WhatsApp{
		cli: whatsmeow.NewClient(device, newLogger(log, "client")),
		db:  db,
		log: log,
	}
	w.cli.AddEventHandler(w.handleEvent)
	return w, nil
}

func (w *WhatsApp) IsPaired() bool { return w.cli.Store.ID != nil }

func (w *WhatsApp) Connect(ctx context.Context) error {
	if !w.IsPaired() {
		return ErrNotPaired
	}
	if err := w.cli.ConnectContext(ctx); err != nil {
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
	qrCh, err := w.cli.GetQRChannel(ctx)
	if err != nil {
		return "", fmt.Errorf("opening pairing channel: %w", err)
	}
	if err := w.cli.ConnectContext(ctx); err != nil {
		return "", fmt.Errorf("connecting for pairing: %w", err)
	}

	select {
	case item, ok := <-qrCh:
		if !ok {
			return "", errors.New("pairing channel closed before it was ready")
		}
		if item.Event != whatsmeow.QRChannelEventCode {
			w.cli.Disconnect()
			return "", fmt.Errorf("pairing not ready: %s", qrFailureReason(item))
		}
	case <-ctx.Done():
		w.cli.Disconnect()
		return "", ctx.Err()
	}

	code, err := w.cli.PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, pairDisplayName)
	if err != nil {
		w.cli.Disconnect()
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

func (w *WhatsApp) Disconnect() { w.cli.Disconnect() }

func (w *WhatsApp) Close() error {
	w.cli.Disconnect()
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
	pn, err := w.cli.Store.LIDs.GetPNForLID(context.Background(), jid)
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
		if h, ok := historyFromProto(v.Data, w.cli.ParseWebMessage, w.resolveKey, w.log); ok {
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

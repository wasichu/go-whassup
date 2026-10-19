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
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
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

	mu            sync.RWMutex
	cli           *whatsmeow.Client // replaced by Logout; read via client()
	opts          Options
	cancelPair    context.CancelFunc // set while a pairing is in progress
	lastGroupSync time.Time          // see groupSyncInterval
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
	cli := whatsmeow.NewClient(device, slogLogger{log: w.log.With("module", "client"), onError: w.clientError})
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

func (w *WhatsApp) SendText(ctx context.Context, chat, text string) error {
	return w.sendText(ctx, chat, &waE2E.Message{Conversation: proto.String(text)}, text, nil)
}

// sendText sends msg (text, or text replying to reply) and emits it.
func (w *WhatsApp) sendText(ctx context.Context, chat string, msg *waE2E.Message, text string, reply *Reply) error {
	cli := w.client()
	if cli.Store.ID == nil {
		return ErrNotPaired
	}
	to, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("bad chat %q: %w", chat, err)
	}
	resp, err := cli.SendMessage(ctx, to, msg)
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
		ReplyTo:   reply,
	})
	return nil
}

func (w *WhatsApp) MarkRead(ctx context.Context, chat string, msgs []Message, mode ReadMode) error {
	if mode == ReadLocal || len(msgs) == 0 {
		return nil
	}
	cli := w.client()
	if cli.Store.ID == nil {
		return ErrNotPaired
	}
	chatJID, err := types.ParseJID(chat)
	if err != nil {
		return fmt.Errorf("bad chat %q: %w", chat, err)
	}
	receipt := types.ReceiptTypeRead
	if mode == ReadDevices {
		receipt = types.ReceiptTypeReadSelf
	}
	// One receipt per sender: whatsmeow requires that, and needs the sender
	// in groups (it's implied in DMs).
	bySender := map[string][]types.MessageID{}
	var order []string
	for _, m := range msgs {
		if m.FromMe {
			continue
		}
		sender := ""
		if chatJID.Server == types.GroupServer {
			sender = m.Sender
		}
		if _, ok := bySender[sender]; !ok {
			order = append(order, sender)
		}
		bySender[sender] = append(bySender[sender], m.ID)
	}
	var errs []error
	for _, sender := range order {
		var senderJID types.JID
		if sender != "" {
			if senderJID, err = types.ParseJID(sender); err != nil {
				errs = append(errs, fmt.Errorf("bad sender %q: %w", sender, err))
				continue
			}
		}
		if err := cli.MarkRead(ctx, bySender[sender], time.Now(), chatJID, senderJID, receipt); err != nil {
			errs = append(errs, fmt.Errorf("marking read in %s: %w", chat, err))
		}
	}
	return errors.Join(errs...)
}

// groupSyncInterval limits full group syncs to one per this long, however
// often the connection drops and comes back.
const groupSyncInterval = time.Hour

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

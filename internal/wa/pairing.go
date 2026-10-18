package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
)

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

// whatsmeow handles a phone-code pairing answer in its own goroutine and,
// when the phone rejects the code, only logs it (no event), so pairing
// would wait for the ~3-minute timeout. clientError spots that log record
// by its format and the typed error, and fails the pairing right away.
// TestCodePairRejectionStillLogged guards these against whatsmeow changes.
const (
	codePairFailedFormat = "Failed to handle code pair notification: %s"
	codePairRejectedTag  = "link_code_pairing_wrapped_primary_ephemeral_pub"
)

// codeRejected is shown when the phone rejects a pairing code.
const codeRejected = "your phone rejected the code. Check the number is the one WhatsApp on that phone uses " +
	"(not WhatsApp Business), then try again with a new code in a few minutes."

func (w *WhatsApp) clientError(format string, args []any) {
	if format != codePairFailedFormat || len(args) != 1 {
		return
	}
	err, _ := args[0].(error)
	var missing *whatsmeow.ElementMissingError
	if !errors.As(err, &missing) || missing.Tag != codePairRejectedTag {
		return
	}
	w.emit(PairFailed{Reason: codeRejected})
	// Stop waiting (it emits nothing more). Not on whatsmeow's goroutine:
	// cancelling may disconnect the client.
	go w.CancelPairing()
}

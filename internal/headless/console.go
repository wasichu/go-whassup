// Package headless implements the non-TUI commands (login, listen). They
// write plain text to Out and are driven entirely through wa.Client, so they
// run against watest.Client in tests.
package headless

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

const (
	// The login websocket closes after ~160s of unanswered pairing; leave headroom.
	defaultPairTimeout = 3 * time.Minute
	eventBuffer        = 64
)

// Console runs headless commands against a terminal-like In/Out.
type Console struct {
	In  io.Reader
	Out io.Writer
	Log *slog.Logger
	// PairTimeout bounds how long Login waits for the code to be entered.
	// Zero means the default.
	PairTimeout time.Duration
}

// Login pairs client via a phone-number code, prompting for the number if
// phone is empty. It returns once the device is linked and connected, with
// the live event subscription (pass it to Follow). If the device is already
// paired it prints a note and returns wa.ErrAlreadyPaired.
func (c Console) Login(ctx context.Context, client wa.Client, phone string) (*wa.Subscription, error) {
	if client.IsPaired() {
		c.printf("Already paired. Run `wa-tui listen` to connect, or `wa-tui login -relink` to pair again.\n")
		return nil, wa.ErrAlreadyPaired
	}
	if phone == "" {
		var err error
		if phone, err = c.prompt("Phone number (with country code, e.g. +1 555 123 4567): "); err != nil {
			return nil, err
		}
	}

	sub := wa.Subscribe(client, eventBuffer, c.Log)
	// ctx (not a timeout ctx) goes to Pair: it bounds the connection's whole
	// lifetime, and the connection outlives this function.
	code, err := client.Pair(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("pairing: %w", err)
	}
	c.printf("\nPairing code: %s\n\n", code)
	c.printf("On your phone: WhatsApp > Settings > Linked devices > Link a device >\n")
	c.printf("\"Link with phone number instead\", then enter the code above.\n")

	timeout := c.PairTimeout
	if timeout == 0 {
		timeout = defaultPairTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case evt := <-sub.C:
			switch v := evt.(type) {
			case wa.PairSuccess:
				c.printf("Linked as %s. Finishing setup...\n", v.JID)
			case wa.Connected:
				// The phone pushes app-state keys and history sync right after
				// linking; callers should keep the connection up (Follow).
				c.printf("Connected and session saved. Staying online for the initial sync;\n")
				c.printf("Ctrl-C when you're done (then use `wa-tui listen`).\n")
				return sub, nil
			case wa.PairFailed:
				return nil, fmt.Errorf("pairing failed: %s", v.Reason)
			case wa.LoggedOut:
				return nil, fmt.Errorf("logged out during pairing: %s", v.Reason)
			}
		case <-deadline.C:
			return nil, errors.New("timed out waiting for the pairing code to be entered")
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for pairing: %w", ctx.Err())
		}
	}
}

// Relink logs out the current session, if any, so Login can pair afresh.
// Stored messages are kept.
func (c Console) Relink(ctx context.Context, client wa.Client) error {
	if !client.IsPaired() {
		c.printf("No existing session; pairing fresh.\n")
		return nil
	}
	c.printf("Unlinking the current session...\n")
	phoneUnlinked, err := client.Logout(ctx)
	if err != nil {
		return fmt.Errorf("unlinking: %w", err)
	}
	if phoneUnlinked {
		c.printf("Removed go-whassup from your phone's Linked devices.\n")
	} else {
		c.printf("Couldn't reach WhatsApp, so only the local session was deleted.\n")
		c.printf("Remove the old go-whassup entry from your phone's Linked devices yourself.\n")
	}
	return nil
}

// Listen connects an already-paired client and prints events until ctx is done.
func (c Console) Listen(ctx context.Context, client wa.Client) error {
	sub := wa.Subscribe(client, eventBuffer, c.Log)
	if err := client.Connect(ctx); err != nil {
		if errors.Is(err, wa.ErrNotPaired) {
			return errors.New("not paired yet; run `wa-tui login` first")
		}
		return err
	}
	c.printf("Listening (Ctrl-C to stop).\n")
	return c.Follow(ctx, sub)
}

// Follow prints events from sub until ctx is done.
func (c Console) Follow(ctx context.Context, sub *wa.Subscription) error {
	var reported uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt := <-sub.C:
			c.printEvent(evt)
		}
		if n := sub.Dropped(); n > reported {
			c.printf("!! dropped %d events (output couldn't keep up)\n", n-reported)
			reported = n
		}
	}
}

// printEvent renders one event. Message text is logged at debug only, to keep
// chat content out of the default log.
func (c Console) printEvent(evt wa.Event) {
	switch v := evt.(type) {
	case wa.Message:
		c.Log.Info("message", "id", v.ID, "chat", v.Chat, "sender", v.Sender, "from_me", v.FromMe, "group", v.IsGroup)
		c.Log.Debug("message text", "id", v.ID, "push_name", v.PushName, "text", v.Text)
		c.printf("%s  %s  %s: %s\n", v.Timestamp.Local().Format("15:04:05"), v.Chat, senderLabel(v), v.Summary())
	case wa.HistorySync:
		c.printf("-- history sync: %d chats, %d messages\n", len(v.Chats), len(v.Messages))
	case wa.LoggedOut:
		c.Log.Warn("logged out", "reason", v.Reason)
		c.printf("Logged out (%s). Run `wa-tui login` to pair again.\n", v.Reason)
	default:
		c.Log.Info("connection event", "type", fmt.Sprintf("%T", evt))
		c.printf("-- %T\n", evt)
	}
}

func senderLabel(m wa.Message) string {
	switch {
	case m.FromMe:
		return "me"
	case m.PushName != "":
		return m.PushName
	default:
		return m.Sender
	}
}

func (c Console) prompt(label string) (string, error) {
	c.printf("%s", label)
	line, err := bufio.NewReader(c.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("no input given")
	}
	return line, nil
}

// printf writes user-facing output. A failed write to the terminal has no
// better place to be reported than the log.
func (c Console) printf(format string, args ...any) {
	if _, err := fmt.Fprintf(c.Out, format, args...); err != nil {
		c.Log.Error("writing output", "err", err)
	}
}

// Command wa-tui is a WhatsApp client for the terminal.
//
// Usage:
//
//	wa-tui [-debug]                 start the TUI
//	wa-tui login [-debug] [phone]   link this device with a phone-number code
//	wa-tui listen [-debug]          connect headless and print incoming messages
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/ui"
	"github.com/wasichu/go-whassup/internal/wa"
)

// The login websocket closes after ~160s of unanswered pairing; leave headroom.
const pairTimeout = 3 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "wa-tui:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "tui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "tui", "login", "listen":
	default:
		return fmt.Errorf("unknown command %q (want login, listen, or none for the TUI)", cmd)
	}
	fs := flag.NewFlagSet("wa-tui "+cmd, flag.ContinueOnError)
	debug := fs.Bool("debug", false, "log debug output (very verbose)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := setup(*debug)
	if err != nil {
		return err
	}
	defer env.logFile.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "tui":
		return runTUI()
	case "login":
		return runLogin(ctx, env, fs.Arg(0))
	default: // "listen"; validated above
		return runListen(ctx, env)
	}
}

type env struct {
	paths   paths.Paths
	log     *slog.Logger
	logFile *os.File
}

func setup(debug bool) (env, error) {
	p, err := paths.Resolve()
	if err != nil {
		return env{}, err
	}
	if err := p.Ensure(); err != nil {
		return env{}, err
	}

	// Bubble Tea owns stdout/stderr while running, so all logging goes to a file.
	logFile, err := os.OpenFile(p.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return env{}, fmt.Errorf("opening log file: %w", err)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	log.Info("starting", "data_dir", p.DataDir)
	return env{paths: p, log: log, logFile: logFile}, nil
}

func (e env) openClient(ctx context.Context) (*wa.WhatsApp, error) {
	return wa.Open(ctx, filepath.Join(e.paths.DataDir, "session.db"), e.log)
}

func runTUI() error {
	if _, err := tea.NewProgram(ui.New()).Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

func runLogin(ctx context.Context, e env, phone string) error {
	ctx, cancel := context.WithTimeout(ctx, pairTimeout)
	defer cancel()

	client, err := e.openClient(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client, e.log)

	if client.IsPaired() {
		fmt.Fprintln(os.Stdout, "Already paired. Run `wa-tui listen` to connect.")
		return nil
	}

	if phone == "" {
		phone, err = prompt(os.Stdin, "Phone number (with country code, e.g. +1 555 123 4567): ")
		if err != nil {
			return err
		}
	}

	events := subscribe(client, e.log)
	code, err := client.Pair(ctx, phone)
	if err != nil {
		return fmt.Errorf("pairing: %w", err)
	}
	fmt.Fprintf(os.Stdout, "\nPairing code: %s\n\n", code)
	fmt.Fprintln(os.Stdout, "On your phone: WhatsApp > Settings > Linked devices > Link a device >")
	fmt.Fprintln(os.Stdout, "\"Link with phone number instead\", then enter the code above.")

	for {
		select {
		case evt := <-events:
			switch v := evt.(type) {
			case wa.PairSuccess:
				fmt.Fprintf(os.Stdout, "Linked as %s. Finishing setup...\n", v.JID)
			case wa.Connected:
				fmt.Fprintln(os.Stdout, "Connected. Session saved; run `wa-tui listen` or `wa-tui`.")
				return nil
			case wa.PairFailed:
				return fmt.Errorf("pairing failed: %s", v.Reason)
			case wa.LoggedOut:
				return fmt.Errorf("logged out during pairing: %s", v.Reason)
			}
		case <-ctx.Done():
			return fmt.Errorf("waiting for pairing: %w", ctx.Err())
		}
	}
}

func runListen(ctx context.Context, e env) error {
	client, err := e.openClient(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client, e.log)

	client.SetEventHandler(func(evt wa.Event) {
		switch v := evt.(type) {
		case wa.Message:
			e.log.Info("message", "id", v.ID, "chat", v.Chat, "sender", v.Sender,
				"push_name", v.PushName, "from_me", v.FromMe, "group", v.IsGroup, "text", v.Text)
			fmt.Fprintf(os.Stdout, "%s  %s  %s: %s\n",
				v.Timestamp.Local().Format("15:04:05"), v.Chat, senderLabel(v), v.Text)
		case wa.LoggedOut:
			e.log.Warn("logged out", "reason", v.Reason)
			fmt.Fprintf(os.Stdout, "Logged out (%s). Run `wa-tui login` to pair again.\n", v.Reason)
		default:
			e.log.Info("connection event", "type", fmt.Sprintf("%T", evt))
			fmt.Fprintf(os.Stdout, "-- %T\n", evt)
		}
	})

	if err := client.Connect(ctx); err != nil {
		if errors.Is(err, wa.ErrNotPaired) {
			return errors.New("not paired yet; run `wa-tui login` first")
		}
		return err
	}
	fmt.Fprintf(os.Stdout, "Listening (Ctrl-C to stop). Also logging to %s\n", e.paths.LogFile)
	<-ctx.Done()
	return nil
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

// subscribe forwards client events to a buffered channel without ever
// blocking whatsmeow's goroutines; overflow is logged and dropped.
func subscribe(client wa.Client, log *slog.Logger) <-chan wa.Event {
	ch := make(chan wa.Event, 64)
	client.SetEventHandler(func(evt wa.Event) {
		select {
		case ch <- evt:
		default:
			log.Warn("dropped event", "type", fmt.Sprintf("%T", evt))
		}
	})
	return ch
}

func prompt(in io.Reader, label string) (string, error) {
	fmt.Fprint(os.Stdout, label)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("no input given")
	}
	return line, nil
}

func closeClient(client wa.Client, log *slog.Logger) {
	if err := client.Close(); err != nil {
		log.Error("closing client", "err", err)
	}
}

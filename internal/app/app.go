// Package app is the composition root: it parses the command line, sets up
// paths and logging, opens the protocol client and runs the chosen mode.
// Logic belongs in the packages it wires together, not here.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/encryption"
	"github.com/wasichu/go-whassup/internal/headless"
	"github.com/wasichu/go-whassup/internal/logging"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/ui"
	"github.com/wasichu/go-whassup/internal/wa"
)

// IO is the process's standard streams.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run executes the command line args until done or ctx is cancelled.
func Run(ctx context.Context, args []string, stdio IO) (err error) {
	inv, err := parseArgs(args, stdio.Err)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if inv.version {
		_, err := fmt.Fprintln(stdio.Out, "go-whassup", version())
		return err
	}
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	if inv.cmd == "db reset" {
		// Before opening (and unlocking) the store: this is the way out
		// when stored messages can't be unlocked.
		return resetMessages(p, stdio.In, stdio.Out)
	}

	var cleanup cleanups
	defer func() { err = errors.Join(err, cleanup.run()) }()
	e, err := openEnv(ctx, inv, p, stdio, &cleanup)
	if err != nil {
		return err
	}
	if strings.HasPrefix(inv.cmd, "db ") {
		return runDB(ctx, e)
	}
	return runOnline(ctx, e)
}

// cleanups runs registered cleanups in reverse order (like defers that
// outlive the function registering them).
type cleanups []func() error

func (c *cleanups) add(f func() error) { *c = append(*c, f) }

func (c cleanups) run() error {
	var errs []error
	for i := len(c) - 1; i >= 0; i-- {
		errs = append(errs, c[i]())
	}
	return errors.Join(errs...)
}

// env is what every command needs once the store is open.
type env struct {
	inv     invocation
	p       paths.Paths
	cfg     config.Config
	log     *slog.Logger
	waLog   *slog.Logger // whatsmeow's: capped by logCap
	logCap  *logging.Cap // info while encrypted: whatsmeow's debug output has message text
	st      *store.Store
	enc     encryption.State
	crypto  *encryption.Controller
	cache   *media.Switchable
	console headless.Console
}

// openEnv loads config, opens the log and the store, sets up encryption
// (starting any background re-seal) and the media cache.
func openEnv(ctx context.Context, inv invocation, p paths.Paths, stdio IO, cleanup *cleanups) (env, error) {
	e := env{inv: inv, p: p}
	var err error
	if e.cfg, err = config.Load(p.ConfigFile); err != nil {
		return e, err
	}
	log, logCloser, err := logging.Open(p.LogFile, inv.debug)
	if err != nil {
		return e, err
	}
	cleanup.add(logCloser.Close)
	slog.SetDefault(log)
	log.Info("starting", "version", version(), "cmd", inv.cmd, "data_dir", p.DataDir, "config", fmt.Sprintf("%+v", e.cfg))
	e.log = log

	if e.st, err = store.Open(ctx, p.MessagesDB, nil); err != nil {
		return e, err
	}
	cleanup.add(e.st.Close)

	// Re-sealing runs while the app does; on exit it's stopped (and
	// resumes next start) before the store closes.
	e.crypto = encryption.New(ctx, e.st, p.MediaDir, realKeySources(e.cfg, p, stdio), log)
	cleanup.add(e.crypto.Close)
	transitions := !strings.HasPrefix(inv.cmd, "db ")
	if e.enc, err = e.crypto.Start(ctx, e.cfg, transitions); err != nil {
		return e, err
	}
	e.cache, e.logCap = e.crypto.Cache(), e.crypto.LogCap()
	e.waLog = logging.Capped(log, e.logCap)
	e.console = headless.Console{In: stdio.In, Out: stdio.Out, Log: log, NoText: e.enc.On}
	return e, nil
}

// runDB runs the offline `db` commands.
func runDB(ctx context.Context, e env) error {
	switch e.inv.cmd {
	case "db chats":
		return e.console.Chats(ctx, e.st)
	case "db messages":
		return e.console.Messages(ctx, e.st, e.inv.args[0], e.inv.limit)
	default: // "db search"; parseArgs rejects anything else
		return e.console.Search(ctx, e.st, strings.Join(e.inv.args, " "), e.inv.limit)
	}
}

// runOnline opens the WhatsApp client, records everything it receives, and
// runs the TUI, login or listen.
func runOnline(ctx context.Context, e env) (err error) {
	client, err := wa.Open(ctx, e.p.SessionDB, e.waLog, pairingOptions(e.cfg))
	if err != nil {
		return err
	}
	defer closeInto(&err, client)

	// The recorder must be registered before any display subscription: the
	// UI reloads from the store when an event arrives, which only works if
	// the recorder (an earlier handler) has already written it. It is never
	// lossy.
	rec := &store.Recorder{Store: e.st, Retention: retention(e.cfg), Log: e.log, Media: e.cache}
	rec.Prune(ctx)
	rec.Attach(client)

	switch e.inv.cmd {
	case "tui":
		deps := tuiDeps(e.cfg, e.p, client, e.st, rec, e.crypto, e.log)
		deps.Notice = encryptionNotice(e.enc)
		return ui.Run(ctx, deps)
	case "login":
		return runLogin(ctx, e, client)
	default: // "listen"; parseArgs rejects anything else
		return e.console.Listen(ctx, client)
	}
}

func runLogin(ctx context.Context, e env, client *wa.WhatsApp) error {
	var phone string
	if len(e.inv.args) > 0 {
		phone = e.inv.args[0]
	}
	if e.inv.relink {
		if err := e.console.Relink(ctx, client); err != nil {
			return err
		}
	}
	sub, err := e.console.Login(ctx, client, phone)
	if errors.Is(err, wa.ErrAlreadyPaired) {
		return nil
	}
	if err != nil {
		return err
	}
	return e.console.Follow(ctx, sub)
}

// encryptionNotice tells the user a background re-seal is running.
func encryptionNotice(e encryption.State) string {
	if !e.Resealing() {
		return ""
	}
	return fmt.Sprintf("Updating encryption of %d stored messages in the background…", e.Pending)
}

// tuiDeps wires the TUI's collaborators. It's a separate function so a test
// can check every field is set: the UI's own tests inject their own deps and
// would not notice a field the app forgot.
func tuiDeps(cfg config.Config, p paths.Paths, client *wa.WhatsApp, st *store.Store, rec *store.Recorder, crypto *encryption.Controller, log *slog.Logger) ui.Deps {
	return ui.Deps{
		Client:     client,
		Store:      st,
		Log:        log,
		Config:     cfg,
		ConfigPath: p.ConfigFile,
		ApplyConfig: func(ctx context.Context, c config.Config) error {
			client.SetOptions(pairingOptions(c))
			rec.SetRetention(retention(c))
			rec.Prune(ctx)
			return nil
		},
		Graphics:   termcaps.DetectGraphics(os.Getenv, termcaps.Tmux),
		FocusHint:  termcaps.FocusHint(os.Getenv, termcaps.Tmux),
		Notify:     termcaps.DetectNotify(os.Getenv, termcaps.Tmux),
		Media:      crypto.Cache(),
		Encryption: crypto,
		OpenFile:   media.Open,
	}
}

func pairingOptions(c config.Config) wa.Options {
	return wa.Options{RequestFullSync: c.History.RequestFullSync, FullSyncDays: c.History.FullSyncDays}
}

func retention(c config.Config) store.Retention {
	return store.Retention{KeepDays: c.History.KeepDays, MaxPerChat: c.History.MaxMessagesPerChat}
}

// closeInto closes c and joins any error into *err, for use with defer.
func closeInto(err *error, c io.Closer) {
	*err = errors.Join(*err, c.Close())
}

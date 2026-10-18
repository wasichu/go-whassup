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

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/headless"
	"github.com/wasichu/go-whassup/internal/logging"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
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

	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	cfg, err := config.Load(p.ConfigFile)
	if err != nil {
		return err
	}
	log, logCloser, err := logging.Open(p.LogFile, inv.debug)
	if err != nil {
		return err
	}
	defer closeInto(&err, logCloser)
	slog.SetDefault(log)
	log.Info("starting", "cmd", inv.cmd, "data_dir", p.DataDir, "config", fmt.Sprintf("%+v", cfg))

	st, err := store.Open(ctx, p.MessagesDB, nil)
	if err != nil {
		return err
	}
	defer closeInto(&err, st)
	console := headless.Console{In: stdio.In, Out: stdio.Out, Log: log}

	switch inv.cmd {
	case "db chats":
		return console.Chats(ctx, st)
	case "db messages":
		return console.Messages(ctx, st, inv.args[0], inv.limit)
	}

	client, err := wa.Open(ctx, p.SessionDB, log, pairingOptions(cfg))
	if err != nil {
		return err
	}
	defer closeInto(&err, client)

	// The recorder must be registered before any display subscription: the
	// UI reloads from the store when an event arrives, which only works if
	// the recorder (an earlier handler) has already written it. It is never
	// lossy.
	rec := &store.Recorder{
		Store:     st,
		Retention: retention(cfg),
		Log:       log,
	}
	rec.Prune(ctx)
	rec.Attach(client)

	switch inv.cmd {
	case "tui":
		return ui.Run(ctx, ui.Deps{
			Client:           client,
			Store:            st,
			Log:              log,
			Config:           cfg,
			ConfigPath:       p.ConfigFile,
			TerminalGraphics: ui.SupportsKittyGraphics(os.Getenv),
			ApplyConfig: func(ctx context.Context, c config.Config) error {
				client.SetOptions(pairingOptions(c))
				rec.SetRetention(retention(c))
				rec.Prune(ctx)
				return nil
			},
		})
	case "login":
		var phone string
		if len(inv.args) > 0 {
			phone = inv.args[0]
		}
		if inv.relink {
			if err := console.Relink(ctx, client); err != nil {
				return err
			}
		}
		sub, err := console.Login(ctx, client, phone)
		if errors.Is(err, wa.ErrAlreadyPaired) {
			return nil
		}
		if err != nil {
			return err
		}
		return console.Follow(ctx, sub)
	default: // "listen"; parseArgs rejects anything else
		return console.Listen(ctx, client)
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

// Package app is the composition root: it parses the command line, sets up
// paths and logging, opens the protocol client and runs the chosen mode.
// Logic belongs in the packages it wires together, not here.
package app

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"

	"github.com/wasichu/go-whassup/internal/headless"
	"github.com/wasichu/go-whassup/internal/logging"
	"github.com/wasichu/go-whassup/internal/paths"
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
	log, logCloser, err := logging.Open(p.LogFile, inv.debug)
	if err != nil {
		return err
	}
	defer closeInto(&err, logCloser)
	slog.SetDefault(log)
	log.Info("starting", "cmd", inv.cmd, "data_dir", p.DataDir)

	if inv.cmd == "tui" {
		return ui.Run(ctx)
	}

	client, err := wa.Open(ctx, p.SessionDB, log)
	if err != nil {
		return err
	}
	defer closeInto(&err, client)

	console := headless.Console{In: stdio.In, Out: stdio.Out, Log: log}
	switch inv.cmd {
	case "login":
		var phone string
		if len(inv.args) > 0 {
			phone = inv.args[0]
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

// closeInto closes c and joins any error into *err, for use with defer.
func closeInto(err *error, c io.Closer) {
	*err = errors.Join(*err, c.Close())
}

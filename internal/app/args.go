package app

import (
	"flag"
	"fmt"
	"io"
)

// invocation is the parsed command line.
type invocation struct {
	cmd   string // "tui", "login" or "listen"
	debug bool
	args  []string // positional arguments after the subcommand
}

const usage = `Usage:
  wa-tui [-debug]                 start the TUI
  wa-tui login [-debug] [phone]   link this device with a phone-number code
  wa-tui listen [-debug]          connect headless and print incoming messages

Flags may come before or after the subcommand.
`

// parseArgs accepts flags before and/or after the subcommand, so
// `wa-tui -debug login` and `wa-tui login -debug` are equivalent. Usage and
// flag errors are written to errOut; -h returns flag.ErrHelp.
func parseArgs(args []string, errOut io.Writer) (invocation, error) {
	var inv invocation
	fs := flag.NewFlagSet("wa-tui", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprint(errOut, usage)
		fs.PrintDefaults()
	}
	fs.BoolVar(&inv.debug, "debug", false, "log debug output (very verbose; includes message text)")
	if err := fs.Parse(args); err != nil {
		return inv, err
	}
	inv.cmd = "tui"
	if fs.NArg() > 0 {
		inv.cmd = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return inv, err
		}
	}
	inv.args = fs.Args()

	maxArgs := map[string]int{"tui": 0, "login": 1, "listen": 0}
	n, ok := maxArgs[inv.cmd]
	if !ok {
		return inv, fmt.Errorf("unknown command %q (want login, listen, or none for the TUI)", inv.cmd)
	}
	if len(inv.args) > n {
		return inv, fmt.Errorf("%s: unexpected arguments %q", inv.cmd, inv.args[n:])
	}
	return inv, nil
}

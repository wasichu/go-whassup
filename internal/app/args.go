package app

import (
	"flag"
	"fmt"
	"io"
)

// invocation is the parsed command line.
type invocation struct {
	cmd    string // "tui", "login", "listen", "db chats" or "db messages"
	debug  bool
	relink bool     // -relink, for "login"
	limit  int      // -n, for "db messages"
	args   []string // positional arguments after the (sub)command
}

const usage = `Usage:
  wa-tui [-debug]                     start the TUI
  wa-tui login [-relink] [phone]      link this device with a phone-number code;
                                      -relink first unlinks the current session
  wa-tui listen [-debug]              connect and print incoming messages
  wa-tui db chats                     list stored chats (reads messages.db; no network)
  wa-tui db messages [-n N] <chat>    show a stored chat's newest N messages;
                                      <chat> is a name, phone number or JID
  wa-tui db search [-n N] <words>     search stored messages (newest N matches)

Flags may come before or after the subcommand.
`

// commands maps each command to its maximum and minimum positional args.
var commands = map[string]struct{ min, max int }{
	"tui":         {0, 0},
	"login":       {0, 1},
	"listen":      {0, 0},
	"db chats":    {0, 0},
	"db messages": {1, 1},
	"db search":   {1, 64}, // words may be passed unquoted
}

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
	fs.BoolVar(&inv.relink, "relink", false, "login: unlink the current session first, then pair again (stored messages are kept)")
	fs.IntVar(&inv.limit, "n", 20, "number of messages to show (db messages; 0 = all)")
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
	if inv.cmd == "db" {
		if fs.NArg() == 0 {
			return inv, fmt.Errorf("db: want a subcommand: chats, messages or search")
		}
		inv.cmd = "db " + fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return inv, err
		}
	}
	inv.args = fs.Args()

	bounds, ok := commands[inv.cmd]
	if !ok {
		return inv, fmt.Errorf("unknown command %q (see wa-tui -h)", inv.cmd)
	}
	if len(inv.args) > bounds.max {
		return inv, fmt.Errorf("%s: unexpected arguments %q", inv.cmd, inv.args[bounds.max:])
	}
	if len(inv.args) < bounds.min {
		return inv, fmt.Errorf("%s: missing argument (see wa-tui -h)", inv.cmd)
	}
	if inv.relink && inv.cmd != "login" {
		return inv, fmt.Errorf("-relink only applies to login")
	}
	if inv.limit < 0 {
		return inv, fmt.Errorf("-n must be 0 or more")
	}
	return inv, nil
}

package app

import (
	"flag"
	"fmt"
	"io"
)

// invocation is the parsed command line.
type invocation struct {
	version bool   // -version: print it and exit
	cmd     string // "tui", "login", "listen", "db chats" or "db messages"
	debug   bool
	relink  bool     // -relink, for "login"
	limit   int      // -n, for "db messages"
	args    []string // positional arguments after the (sub)command
}

const usage = `Usage:
  go-whassup [-debug]                     start the TUI
  go-whassup login [-relink] [phone]      link this device with a phone-number code;
                                          -relink first unlinks the current session
  go-whassup listen [-debug]              connect and print incoming messages
  go-whassup db chats                     list stored chats (reads messages.db; no network)
  go-whassup db messages [-n N] <chat>    show a stored chat's newest N messages;
                                          <chat> is a name, phone number or JID
  go-whassup db search [-n N] <words>     search stored messages (newest N matches)
  go-whassup db reset                     delete stored messages and media (asks first)

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
	"db reset":    {0, 0},
}

// parseArgs accepts flags before and/or after the subcommand, so
// `go-whassup -debug login` and `go-whassup login -debug` are equivalent. Usage and
// flag errors are written to errOut; -h returns flag.ErrHelp.
func parseArgs(args []string, errOut io.Writer) (invocation, error) {
	var inv invocation
	fs := flag.NewFlagSet("go-whassup", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprint(errOut, usage)
		fs.PrintDefaults()
	}
	fs.BoolVar(&inv.debug, "debug", false, "log debug output (very verbose; includes message text)")
	fs.BoolVar(&inv.relink, "relink", false, "login: unlink the current session first, then pair again (stored messages are kept)")
	fs.IntVar(&inv.limit, "n", 20, "number of messages to show (db messages; 0 = all)")
	fs.BoolVar(&inv.version, "version", false, "print the version and exit")
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
			return inv, fmt.Errorf("db: want a subcommand: chats, messages, search or reset")
		}
		inv.cmd = "db " + fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return inv, err
		}
	}
	inv.args = fs.Args()

	bounds, ok := commands[inv.cmd]
	if !ok {
		return inv, fmt.Errorf("unknown command %q (see go-whassup -h)", inv.cmd)
	}
	if len(inv.args) > bounds.max {
		return inv, fmt.Errorf("%s: unexpected arguments %q", inv.cmd, inv.args[bounds.max:])
	}
	if len(inv.args) < bounds.min {
		return inv, fmt.Errorf("%s: missing argument (see go-whassup -h)", inv.cmd)
	}
	if inv.relink && inv.cmd != "login" {
		return inv, fmt.Errorf("-relink only applies to login")
	}
	if inv.limit < 0 {
		return inv, fmt.Errorf("-n must be 0 or more")
	}
	return inv, nil
}

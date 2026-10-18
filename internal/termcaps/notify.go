package termcaps

import "strings"

// NotifyProtocol is the escape sequence a terminal understands for desktop
// notifications.
type NotifyProtocol int

const (
	NotifyNone   NotifyProtocol = iota // nothing reaches the terminal (see Hint)
	NotifyOSC9                         // iTerm2, WezTerm, Windows Terminal, …: body only
	NotifyOSC777                       // Ghostty, foot, urxvt: title and body
	NotifyOSC99                        // kitty: title and body
)

// Notify describes how to raise a desktop notification through the
// terminal (which shows it natively; clicking it focuses the terminal).
type Notify struct {
	Protocol NotifyProtocol
	Tmux     bool // wrap in tmux passthrough
	// Hint explains why notifications can't get through, when something
	// in between (tmux) is in the way. Empty otherwise.
	Hint string
}

// DetectNotify picks the notification protocol for the terminal described
// by getenv, asking tmux (via run) about the outer terminal when inside it.
// Unknown terminals get OSC 9, the most widely supported form; terminals
// that don't support it ignore it.
func DetectNotify(getenv func(string) string, run Runner) Notify {
	if getenv("TMUX") == "" {
		if strings.HasPrefix(getenv("TERM"), "screen") {
			return Notify{} // GNU screen swallows these
		}
		return Notify{Protocol: notifyProtocol(getenv("TERM"), getenv("TERM_PROGRAM"), getenv("KITTY_WINDOW_ID"))}
	}
	passthrough, err := run("show-options", "-gv", "allow-passthrough")
	if err != nil || (passthrough != "on" && passthrough != "all") {
		return Notify{Hint: "For desktop notifications through tmux, enable passthrough: " +
			"tmux set -g allow-passthrough on (add it to ~/.tmux.conf to keep it)."}
	}
	outer, err := run("display-message", "-p", "#{client_termname}")
	if err != nil {
		outer = ""
	}
	return Notify{Protocol: notifyProtocol(outer, "", ""), Tmux: true}
}

func notifyProtocol(term, program, kittyWindow string) NotifyProtocol {
	switch {
	case term == "xterm-kitty" || kittyWindow != "":
		return NotifyOSC99
	case program == "ghostty" || term == "xterm-ghostty",
		strings.HasPrefix(term, "foot"), strings.HasPrefix(term, "rxvt"):
		return NotifyOSC777
	}
	return NotifyOSC9
}

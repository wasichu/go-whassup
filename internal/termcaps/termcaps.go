// Package termcaps detects what the user's terminal can do beyond text,
// including through tmux. It only inspects the environment and asks tmux
// about the current client; it never changes any configuration.
package termcaps

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Graphics describes inline-image support (Kitty graphics protocol with
// Unicode placeholders).
type Graphics struct {
	Kitty bool // images can be shown
	Tmux  bool // escape sequences must be wrapped in tmux passthrough
	// Hint explains how to enable images when the terminal could show them
	// but something in between (tmux) is in the way. Empty otherwise.
	Hint string
}

// Runner runs a tmux command and returns its trimmed output.
type Runner func(args ...string) (string, error)

// Tmux runs the real tmux binary with a short timeout.
func Tmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// DetectGraphics reports inline-image support for the terminal described by
// getenv, asking tmux (via run) about the outer terminal when inside it.
func DetectGraphics(getenv func(string) string, run Runner) Graphics {
	if strings.HasPrefix(getenv("TERM"), "screen") && getenv("TMUX") == "" {
		return Graphics{} // GNU screen: no passthrough support we rely on
	}
	if getenv("TMUX") == "" {
		return Graphics{Kitty: kittyTerminal(getenv("TERM"), getenv("TERM_PROGRAM"), getenv("KITTY_WINDOW_ID"))}
	}

	// Inside tmux, TERM and TERM_PROGRAM describe tmux, so ask it which
	// terminal the current client is attached from.
	outer, err := run("display-message", "-p", "#{client_termname}")
	if err != nil || !kittyTerminal(outer, "", "") {
		return Graphics{}
	}
	features, err := run("display-message", "-p", "#{client_termfeatures}")
	if err != nil {
		return Graphics{}
	}
	if !hasFeature(features, "RGB") {
		return Graphics{Hint: "For a small inline QR image, let tmux pass 24-bit color: " +
			"set -as terminal-features '," + outer + ":RGB' (then restart tmux)."}
	}
	passthrough, err := run("show-options", "-gv", "allow-passthrough")
	if err != nil || (passthrough != "on" && passthrough != "all") {
		return Graphics{Hint: "For a small inline QR image, enable tmux passthrough: " +
			"tmux set -g allow-passthrough on (add it to ~/.tmux.conf to keep it)."}
	}
	return Graphics{Kitty: true, Tmux: true}
}

func kittyTerminal(term, program, kittyWindow string) bool {
	return program == "ghostty" || term == "xterm-ghostty" || term == "xterm-kitty" || kittyWindow != ""
}

func hasFeature(list, want string) bool {
	for _, f := range strings.Split(list, ",") {
		if strings.TrimSpace(f) == want {
			return true
		}
	}
	return false
}

// FocusHint returns advice if the terminal can't report focus changes to
// us, which go-whassup uses to avoid marking chats read while you're in
// another window, and to notify only then. Only tmux needs it (focus-events is off by default);
// "" means focus reporting should work.
func FocusHint(getenv func(string) string, run Runner) string {
	if getenv("TMUX") == "" {
		return ""
	}
	v, err := run("show-options", "-gv", "focus-events")
	if err != nil || v == "on" {
		return ""
	}
	return "tmux isn't passing focus changes, so chats open here count as read and notifications never fire while you're in another window: tmux set -g focus-events on"
}

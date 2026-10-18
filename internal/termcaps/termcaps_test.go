package termcaps

import (
	"errors"
	"strings"
	"testing"
)

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

// fakeTmux answers the queries DetectGraphics makes.
func fakeTmux(termname, features, passthrough string) Runner {
	return func(args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "display-message -p #{client_termname}":
			return termname, nil
		case "display-message -p #{client_termfeatures}":
			return features, nil
		case "show-options -gv allow-passthrough":
			return passthrough, nil
		case "show-options -gv focus-events":
			return "off", nil
		}
		return "", errors.New("unexpected tmux call: " + strings.Join(args, " "))
	}
}

func noTmux(...string) (string, error) { return "", errors.New("tmux must not be called") }

func TestDetectGraphics(t *testing.T) {
	inTmux := map[string]string{"TMUX": "/tmp/tmux-501/default,1,0", "TERM": "tmux-256color", "TERM_PROGRAM": "tmux"}
	tests := []struct {
		name     string
		env      map[string]string
		run      Runner
		want     Graphics
		wantHint string
	}{
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, noTmux, Graphics{Kitty: true}, ""},
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, noTmux, Graphics{Kitty: true}, ""},
		{"apple terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, noTmux, Graphics{}, ""},
		{"gnu screen", map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "ghostty"}, noTmux, Graphics{}, ""},
		{"tmux in ghostty, ready", inTmux, fakeTmux("xterm-ghostty", "bpaste,RGB,title", "on"), Graphics{Kitty: true, Tmux: true}, ""},
		{"tmux passthrough all", inTmux, fakeTmux("xterm-kitty", "RGB", "all"), Graphics{Kitty: true, Tmux: true}, ""},
		{"tmux passthrough off", inTmux, fakeTmux("xterm-ghostty", "bpaste,RGB,title", "off"), Graphics{}, "allow-passthrough on"},
		{"tmux without RGB", inTmux, fakeTmux("xterm-ghostty", "bpaste,title", "on"), Graphics{}, "xterm-ghostty:RGB"},
		{"tmux in other terminal", inTmux, fakeTmux("xterm-256color", "RGB", "on"), Graphics{}, ""},
		{"tmux unreachable", inTmux, noTmux, Graphics{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectGraphics(env(tt.env), tt.run)
			if got.Kitty != tt.want.Kitty || got.Tmux != tt.want.Tmux {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if tt.wantHint == "" && got.Hint != "" || !strings.Contains(got.Hint, tt.wantHint) {
				t.Errorf("hint = %q, want containing %q", got.Hint, tt.wantHint)
			}
		})
	}
}

func TestFocusHint(t *testing.T) {
	inTmux := env(map[string]string{"TMUX": "/tmp/tmux-501/default,1,0"})
	focus := func(v string) Runner {
		return func(args ...string) (string, error) { return v, nil }
	}
	if h := FocusHint(env(map[string]string{}), noTmux); h != "" {
		t.Errorf("outside tmux: %q", h)
	}
	if h := FocusHint(inTmux, focus("on")); h != "" {
		t.Errorf("focus-events on: %q", h)
	}
	if h := FocusHint(inTmux, focus("off")); !strings.Contains(h, "tmux set -g focus-events on") {
		t.Errorf("focus-events off: %q", h)
	}
}

func TestDetectNotify(t *testing.T) {
	vars := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	tmux := func(passthrough, outer string) Runner {
		return func(args ...string) (string, error) {
			if args[0] == "show-options" {
				return passthrough, nil
			}
			return outer, nil
		}
	}
	noRun := func(...string) (string, error) { t.Fatal("tmux asked outside tmux"); return "", nil }
	for _, tt := range []struct {
		name string
		env  func(string) string
		run  Runner
		want Notify
	}{
		{"ghostty", vars("TERM", "xterm-ghostty"), noRun, Notify{Protocol: NotifyOSC777}},
		{"ghostty by program", vars("TERM", "xterm-256color", "TERM_PROGRAM", "ghostty"), noRun, Notify{Protocol: NotifyOSC777}},
		{"kitty", vars("TERM", "xterm-kitty"), noRun, Notify{Protocol: NotifyOSC99}},
		{"foot", vars("TERM", "foot"), noRun, Notify{Protocol: NotifyOSC777}},
		{"iterm/unknown", vars("TERM", "xterm-256color", "TERM_PROGRAM", "iTerm.app"), noRun, Notify{Protocol: NotifyOSC9}},
		{"screen", vars("TERM", "screen-256color"), noRun, Notify{}},
		{"tmux in ghostty", vars("TMUX", "/tmp/t", "TERM", "tmux-256color"), tmux("on", "xterm-ghostty"), Notify{Protocol: NotifyOSC777, Tmux: true}},
		{"tmux unknown outer", vars("TMUX", "/tmp/t"), tmux("all", "xterm-256color"), Notify{Protocol: NotifyOSC9, Tmux: true}},
	} {
		if got := DetectNotify(tt.env, tt.run); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
	got := DetectNotify(vars("TMUX", "/tmp/t"), tmux("off", "xterm-ghostty"))
	if got.Protocol != NotifyNone || !strings.Contains(got.Hint, "allow-passthrough on") {
		t.Errorf("passthrough off: %+v", got)
	}
}

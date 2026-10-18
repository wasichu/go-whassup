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

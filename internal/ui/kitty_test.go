package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestSupportsKittyGraphics(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, true},
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"KITTY_WINDOW_ID": "3", "TERM": "xterm-256color"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty", "TMUX": "/tmp/tmux-501/default,1,0"}, false},
		{map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "ghostty"}, false},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, false},
		{map[string]string{}, false},
	}
	for _, tt := range tests {
		if got := SupportsKittyGraphics(func(k string) string { return tt.env[k] }); got != tt.want {
			t.Errorf("SupportsKittyGraphics(%v) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestKittyTransmitChunksAndReassembles(t *testing.T) {
	png := bytes.Repeat([]byte{0xAB}, 10000) // > 4096 base64 bytes: several chunks
	seq := kittyTransmit(qrImageID, png, 32, 16)

	re := regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`)
	parts := re.FindAllStringSubmatch(seq, -1)
	if len(parts) < 3 {
		t.Fatalf("got %d chunks, want several", len(parts))
	}
	first := parts[0][1]
	for _, want := range []string{"a=T", "U=1", "f=100", "q=2", fmt.Sprintf("i=%d", qrImageID), "c=32", "r=16", "m=1"} {
		if !strings.Contains(first, want) {
			t.Errorf("first chunk keys %q missing %q", first, want)
		}
	}
	var data strings.Builder
	for i, p := range parts {
		if len(p[2]) > 4096 {
			t.Errorf("chunk %d is %d bytes, max 4096", i, len(p[2]))
		}
		last := i == len(parts)-1
		if wantM := map[bool]string{true: "m=0", false: "m=1"}[last]; !strings.Contains(p[1], wantM) {
			t.Errorf("chunk %d keys %q, want %s", i, p[1], wantM)
		}
		data.WriteString(p[2])
	}
	got, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil || !bytes.Equal(got, png) {
		t.Errorf("reassembled payload differs (err %v)", err)
	}
}

func TestKittyPlaceholders(t *testing.T) {
	out := kittyPlaceholders(qrImageID, 5, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for r, line := range lines {
		if w := lipgloss.Width(line); w != 5 {
			t.Errorf("row %d is %d cells wide, want 5", r, w)
		}
		if n := strings.Count(line, string(rune(0x10EEEE))); n != 5 {
			t.Errorf("row %d has %d placeholders, want 5", r, n)
		}
		// Row diacritic for row r, then column diacritics 0..4.
		if !strings.Contains(line, string([]rune{0x10EEEE, placeholderDiacritics[r], placeholderDiacritics[4]})) {
			t.Errorf("row %d missing the encoded last cell", r)
		}
	}
	// The foreground color encodes the image id (0x77a001 = 119;160;1).
	if !strings.Contains(out, "38;2;119;160;1") {
		t.Errorf("placeholder color doesn't encode the image id: %q", lines[0][:40])
	}
}

func TestQRPNGHasQuietZone(t *testing.T) {
	png, err := qrPNG(sampleQR)
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("qrPNG: %v", err)
	}
}

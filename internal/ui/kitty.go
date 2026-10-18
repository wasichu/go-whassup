package ui

import (
	"encoding/base64"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"rsc.io/qr"
)

// Inline images use the Kitty graphics protocol with Unicode placeholders
// (https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders):
// the image is transmitted once with a raw escape sequence, then shown by
// ordinary text cells (U+10EEEE plus row/column diacritics) whose foreground
// color encodes the image ID. Because the placement is just text, it
// survives Bubble Tea's redraws, resizes and layout changes. Supported by
// Ghostty and kitty, also through tmux with passthrough (the placeholders are
// what make tmux work: tmux only needs to forward the upload).

// qrImageID identifies our QR image in the terminal. It must fit in 24 bits
// because the placeholder's RGB foreground color encodes it.
const qrImageID = 0x77A001

// Size of the inline QR in cells. Terminal cells are about twice as tall as
// wide, so 2:1 columns:rows is roughly square on screen; the terminal scales
// the PNG to fill exactly this area.
const qrImageCols, qrImageRows = 32, 16

// qrPNG encodes text as a QR PNG, including the spec's 4-module quiet zone.
func qrPNG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, fmt.Errorf("encoding QR: %w", err)
	}
	code.Scale = 4
	return code.PNG(), nil
}

// kittyTransmit returns the escape sequence that uploads png as image id and
// creates a virtual placement of cols x rows cells for placeholders to show.
// Payloads are sent in 4096-byte base64 chunks as the protocol requires;
// q=2 suppresses the terminal's replies so they don't arrive as input.
func kittyTransmit(id uint32, png []byte, cols, rows int, tmux bool) string {
	data := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	for first := true; first || data != ""; first = false {
		chunk := data
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		data = data[len(chunk):]
		more := 0
		if data != "" {
			more = 1
		}
		if first {
			b.WriteString(passthrough(fmt.Sprintf("\x1b_Ga=T,U=1,f=100,t=d,q=2,i=%d,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk), tmux))
		} else {
			b.WriteString(passthrough(fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, chunk), tmux))
		}
	}
	return b.String()
}

// kittyDelete frees image id and its placements in the terminal.
func kittyDelete(id uint32, tmux bool) string {
	return passthrough(fmt.Sprintf("\x1b_Ga=d,d=I,q=2,i=%d\x1b\\", id), tmux)
}

// passthrough wraps an escape sequence so tmux forwards it to the outer
// terminal (requires allow-passthrough): DCS tmux; with ESCs doubled.
func passthrough(seq string, tmux bool) string {
	if !tmux {
		return seq
	}
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// kittyPlaceholders renders the cols x rows block of placeholder cells that
// displays image id.
func kittyPlaceholders(id uint32, cols, rows int) string {
	// Rows and columns are encoded by diacritics from a fixed table, so a
	// placement can't be bigger than it (callers size within maxPlacement).
	cols, rows = min(cols, maxPlacement), min(rows, maxPlacement)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("#%06x", id&0xFFFFFF)))
	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		for c := range cols {
			b.WriteRune(0x10EEEE)
			b.WriteRune(placeholderDiacritics[r])
			b.WriteRune(placeholderDiacritics[c])
		}
		lines[r] = style.Render(b.String())
	}
	return strings.Join(lines, "\n")
}

// placeholderDiacritics are the first entries of kitty's rowcolumn-diacritics
// table (gen/rowcolumn-diacritics.txt): the Nth encodes row or column N.
// maxPlacement is the largest placement, in cells, placeholders can address.
const maxPlacement = len(placeholderDiacritics)

var placeholderDiacritics = [...]rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611,
	0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
}

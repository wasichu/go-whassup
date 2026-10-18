package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"rsc.io/qr"
)

// qrQuietZones are the white borders tried, in modules, largest first. The
// spec asks for 4; phones scan 2 reliably and usually 1, and each module of
// border costs a whole terminal column and half a row on every side.
var qrQuietZones = []int{2, 1}

// QR colors are fixed, not themed: scanners need dark modules on a light
// background, and terminal themes remap ANSI black/white.
var (
	qrDark  = lipgloss.Color("#000000")
	qrLight = lipgloss.Color("#ffffff")
)

// qrMatrix encodes text and returns its modules (true = dark) including a
// quiet zone of the given width. It uses the lowest error-correction level
// for the smallest code.
func qrMatrix(text string, qrQuiet int) ([][]bool, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, fmt.Errorf("encoding QR: %w", err)
	}
	n := code.Size + 2*qrQuiet
	m := make([][]bool, n)
	for y := range m {
		m[y] = make([]bool, n)
		for x := range m[y] {
			cx, cy := x-qrQuiet, y-qrQuiet
			if cx >= 0 && cy >= 0 && cx < code.Size && cy < code.Size {
				m[y][x] = code.Black(cx, cy)
			}
		}
	}
	return m, nil
}

// halfBlocks renders a module matrix two rows per text line using "▀": the
// foreground paints the top module, the background the bottom one. The
// result is len(m) columns by ceil(len(m)/2) lines.
func halfBlocks(m [][]bool) string {
	color := func(dark bool) lipgloss.Style {
		if dark {
			return lipgloss.NewStyle().Foreground(qrDark)
		}
		return lipgloss.NewStyle().Foreground(qrLight)
	}
	var b strings.Builder
	for y := 0; y < len(m); y += 2 {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := range m[y] {
			bottom := false // an odd final row gets a light bottom half
			if y+1 < len(m) {
				bottom = m[y+1][x]
			}
			bg := qrLight
			if bottom {
				bg = qrDark
			}
			b.WriteString(color(m[y][x]).Background(bg).Render("▀"))
		}
	}
	return b.String()
}

// renderQR returns the QR for text and its size in cells.
func renderQR(text string, quiet int) (out string, width, height int, err error) {
	m, err := qrMatrix(text, quiet)
	if err != nil {
		return "", 0, 0, err
	}
	return halfBlocks(m), len(m), (len(m) + 1) / 2, nil
}

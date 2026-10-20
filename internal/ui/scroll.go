package ui

import (
	"fmt"
	"slices"
)

// Scrolling a list of lines within a page, with a cue when some are out of
// view: the member list (info) and the reactions on the i card.

// maxScroll is how far lines can scroll in rows: until the last line
// shows above the "more" line.
func maxScroll(n, rows int) int {
	if n <= rows {
		return 0
	}
	return n - (rows - 1)
}

// scrollWindow is the rows of lines shown from offset; when they don't all
// fit, the last row says how many more are below (or above, at the end).
func scrollWindow(lines []string, offset, rows int) []string {
	if len(lines) <= rows {
		return lines
	}
	start := min(offset, maxScroll(len(lines), rows))
	visible := rows - 1
	out := slices.Clone(lines[start : start+visible])
	if below := len(lines) - start - visible; below > 0 {
		return append(out, faintStyle.Render(fmt.Sprintf("  ↓ %d more (↑/↓ scroll)", below)))
	}
	return append(out, faintStyle.Render(fmt.Sprintf("  ↑ %d more (↑/↓ scroll)", start)))
}

package ui

import (
	"strings"
	"testing"
)

// The shape of a real QR payload as wa emits it (ref, noise key, identity
// key, adv secret, platform; link prefix stripped), which encodes to 61x31
// cells.
var sampleQR = "2@" + strings.Repeat("A", 100) + "," +
	strings.Repeat("B", 44) + "," + strings.Repeat("C", 44) + "," + strings.Repeat("D", 44) + ",9"

func TestQRMatrixStructure(t *testing.T) {
	const qrQuiet = 2
	m, err := qrMatrix(sampleQR, qrQuiet)
	if err != nil {
		t.Fatalf("qrMatrix: %v", err)
	}
	n := len(m)
	for y, row := range m {
		if len(row) != n {
			t.Fatalf("row %d has %d modules, want %d (square)", y, len(row), n)
		}
	}
	// Quiet zone: the outer border is all light.
	for i := range n {
		for q := range qrQuiet {
			if m[q][i] || m[n-1-q][i] || m[i][q] || m[i][n-1-q] {
				t.Fatalf("quiet zone has a dark module near (%d,%d)", i, q)
			}
		}
	}
	// Top-left finder pattern: 7x7 dark ring, light ring, 3x3 dark center.
	o := qrQuiet
	for y := range 7 {
		for x := range 7 {
			ring := x == 0 || y == 0 || x == 6 || y == 6
			center := x >= 2 && x <= 4 && y >= 2 && y <= 4
			if want := ring || center; m[o+y][o+x] != want {
				t.Fatalf("finder module (%d,%d) = %v, want %v", x, y, m[o+y][o+x], want)
			}
		}
	}
	if n != 61 {
		t.Errorf("realistic payload is %d modules wide, want 61 (update the layout tests if this changes)", n)
	}
}

func TestRenderQRSize(t *testing.T) {
	out, w, h, err := renderQR(sampleQR, 2)
	if err != nil {
		t.Fatalf("renderQR: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Errorf("lines = %d, want %d", len(lines), h)
	}
	if got := strings.Count(lines[0], "▀"); got != w {
		t.Errorf("first line has %d cells, want %d", got, w)
	}
}

func TestHalfBlocksOddRows(t *testing.T) {
	out := halfBlocks([][]bool{{true}, {false}, {true}})
	if got := strings.Count(out, "\n") + 1; got != 2 {
		t.Errorf("3 rows rendered as %d lines, want 2", got)
	}
}

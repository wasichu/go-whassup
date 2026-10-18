package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // registers the decoder
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// File previews in the send-file browser are shown like the login QR: as a
// Kitty-protocol image behind Unicode placeholders (see kitty.go).

// previewImageID identifies the browser's preview image in the terminal.
const previewImageID = 0x77A002

// Assumed cell size in pixels: terminal cells are about twice as tall as
// wide. Previews are letterboxed to the cell area's shape, so the terminal
// never stretches them whatever it assumes.
const cellW, cellH = 10, 20

const (
	maxPreviewFile   = 40 << 20 // bytes; bigger files aren't decoded
	maxPreviewPixels = 60e6     // decoded size limit (width × height)
)

// errNoPreview means the file isn't an image we can preview.
var errNoPreview = errors.New("no preview for this file")

// canPreview reports whether path looks like an image we decode (the
// standard library's JPEG, PNG and GIF; not WebP or HEIC).
func canPreview(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	}
	return false
}

// previewPNG decodes the image at path and returns a PNG of the cols×rows
// cell area: the image scaled to fit, centered on a transparent canvas.
func previewPNG(path string, cols, rows int) ([]byte, error) {
	if !canPreview(path) || cols < 1 || rows < 1 {
		return nil, errNoPreview
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > maxPreviewFile {
		return nil, errNoPreview
	}
	return previewFrom(f, cols, rows)
}

// previewBytes is previewPNG for an image held in memory (WhatsApp's
// embedded thumbnails).
func previewBytes(data []byte, cols, rows int) ([]byte, error) {
	if len(data) == 0 || cols < 1 || rows < 1 {
		return nil, errNoPreview
	}
	return previewFrom(bytes.NewReader(data), cols, rows)
}

func previewFrom(r io.ReadSeeker, cols, rows int) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil || cfg.Width*cfg.Height > maxPreviewPixels || cfg.Width < 1 || cfg.Height < 1 {
		return nil, errNoPreview
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, letterbox(src, cols*cellW, rows*cellH)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// letterbox scales src to fit w×h, keeping its aspect ratio, and centers it
// on a transparent w×h canvas. Each output pixel averages a 2×2 grid of
// samples: plenty for a preview, and fast on large photos.
func letterbox(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	scale := min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
	sw, sh := max(int(float64(b.Dx())*scale), 1), max(int(float64(b.Dy())*scale), 1)
	ox, oy := (w-sw)/2, (h-sh)/2
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range sh {
		for x := range sw {
			var r, g, bl, a uint32
			for _, d := range [4][2]float64{{0.25, 0.25}, {0.75, 0.25}, {0.25, 0.75}, {0.75, 0.75}} {
				sx := b.Min.X + int((float64(x)+d[0])/scale)
				sy := b.Min.Y + int((float64(y)+d[1])/scale)
				cr, cg, cb, ca := src.At(min(sx, b.Max.X-1), min(sy, b.Max.Y-1)).RGBA()
				r, g, bl, a = r+cr, g+cg, bl+cb, a+ca
			}
			dst.Set(ox+x, oy+y, color.RGBA64{R: uint16(r / 4), G: uint16(g / 4), B: uint16(bl / 4), A: uint16(a / 4)})
		}
	}
	return dst
}

// previewer shows one preview image beside a list (the file browser, the
// attachments view): it waits for the cursor to rest on an entry, has the
// page decode it off the UI goroutine, shows the result unless the cursor
// has moved on, and frees the image when it's no longer shown.
type previewer struct {
	owner string // tags its messages, so a closed page's results are dropped
	seq   int    // generation; results for older ones are dropped
	shown string // key of the preview in the terminal, "" if none
	cols  int    // its size in cells
	rows  int
	tmux  bool
}

type (
	previewTickMsg struct {
		owner string
		seq   int
	}
	previewReadyMsg struct {
		owner      string
		seq        int
		key        string
		png        []byte
		cols, rows int
		err        error
	}
)

// moved is called when the highlighted entry changes: it drops the shown
// preview and, if want, waits previewDelay before asking for a new one.
func (p *previewer) moved(want bool) tea.Cmd {
	p.seq++
	hide := p.hide()
	if !want {
		return hide
	}
	owner, seq := p.owner, p.seq
	return tea.Batch(hide, tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewTickMsg{owner: owner, seq: seq} }))
}

// due reports whether a tick is for the current highlight.
func (p *previewer) due(msg previewTickMsg) bool { return msg.owner == p.owner && msg.seq == p.seq }

// load decodes a preview of key off the UI goroutine with decode.
func (p *previewer) load(key string, cols, rows int, decode func(cols, rows int) ([]byte, error)) tea.Cmd {
	owner, seq := p.owner, p.seq
	return func() tea.Msg {
		png, err := decode(cols, rows)
		return previewReadyMsg{owner: owner, seq: seq, key: key, png: png, cols: cols, rows: rows, err: err}
	}
}

// ready shows a decoded preview, unless it's stale or failed.
func (p *previewer) ready(msg previewReadyMsg) tea.Cmd {
	if msg.owner != p.owner || msg.seq != p.seq || msg.err != nil {
		return nil
	}
	p.shown, p.cols, p.rows = msg.key, msg.cols, msg.rows
	return tea.Raw(kittyTransmit(previewImageID, msg.png, msg.cols, msg.rows, p.tmux))
}

// hide frees the preview image, if one is in the terminal.
func (p *previewer) hide() tea.Cmd {
	if p.shown == "" {
		return nil
	}
	p.shown = ""
	return tea.Raw(kittyDelete(previewImageID, p.tmux))
}

// showing reports whether key's preview is in the terminal.
func (p *previewer) showing(key string) bool { return key != "" && p.shown == key }

// placeholders draws the shown preview.
func (p *previewer) placeholders() string { return kittyPlaceholders(previewImageID, p.cols, p.rows) }

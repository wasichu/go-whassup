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
	"os"
	"path/filepath"
	"strings"
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
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width*cfg.Height > maxPreviewPixels || cfg.Width < 1 || cfg.Height < 1 {
		return nil, errNoPreview
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", filepath.Base(path), err)
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

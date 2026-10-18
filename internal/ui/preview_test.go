package ui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestLetterboxKeepsAspect(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	out := letterbox(solid(400, 200, red), 100, 100) // 2:1 into a square
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 100 {
		t.Fatalf("size = %v", out.Bounds())
	}
	if _, _, _, a := out.At(50, 10).RGBA(); a != 0 {
		t.Error("band above a wide image should be transparent")
	}
	if r, _, _, a := out.At(50, 50).RGBA(); a != 0xffff || r != 0xffff {
		t.Errorf("center = %v, want red", out.At(50, 50))
	}
	if _, _, _, a := out.At(50, 80).RGBA(); a != 0 {
		t.Error("band below should be transparent")
	}
}

func TestPreviewPNG(t *testing.T) {
	dir := t.TempDir()
	photo := filepath.Join(dir, "photo.JPG")
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solid(64, 48, color.RGBA{G: 200, A: 255}), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(photo, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := previewPNG(photo, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 8*cellW || img.Bounds().Dy() != 3*cellH {
		t.Fatalf("preview = %v, %v; want the 8×3-cell area in pixels", img.Bounds(), err)
	}

	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake.png") // the extension lies
	if err := os.WriteFile(fake, []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := previewPNG(notes, 8, 3); !errors.Is(err, errNoPreview) {
		t.Errorf("text file: %v", err)
	}
	if _, err := previewPNG(fake, 8, 3); !errors.Is(err, errNoPreview) {
		t.Errorf("fake png: %v", err)
	}
}

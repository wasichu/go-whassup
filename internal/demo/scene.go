package demo

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// The demo's photo is drawn, not shipped: a sunset over mountains. No
// real picture means nothing to license and nobody's data.

func scene(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sunX, sunY, sunR := float64(w)*0.68, float64(h)*0.42, float64(h)*0.12
	for y := range h {
		fy := float64(y) / float64(h)
		for x := range w {
			fx := float64(x) / float64(w)
			// Sky: deep blue fading to orange near the horizon.
			c := color.RGBA{R: uint8(40 + 200*fy), G: uint8(60 + 90*fy), B: uint8(140 - 80*fy), A: 255}
			if dx, dy := float64(x)-sunX, float64(y)-sunY; dx*dx+dy*dy < sunR*sunR {
				c = color.RGBA{R: 255, G: 210, B: 90, A: 255}
			}
			// Two ridges, the nearer one darker.
			if fy > 0.55+0.25*abs(fx-0.3)-0.1 {
				c = color.RGBA{R: 70, G: 60, B: 90, A: 255}
			}
			if fy > 0.70+0.20*abs(fx-0.75)-0.05 {
				c = color.RGBA{R: 35, G: 40, B: 55, A: 255}
			}
			// A lake reflecting the sky.
			if fy > 0.85 {
				c = color.RGBA{R: uint8(60 + 120*(1-fy)), G: uint8(80 + 60*(1-fy)), B: 130, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// sceneJPEG is the photo as WhatsApp's small embedded thumbnail.
func sceneJPEG(w, h int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, scene(w, h), &jpeg.Options{Quality: 70}); err != nil {
		return nil // can't happen for an in-memory RGBA image
	}
	return b.Bytes()
}

// scenePNG is the photo as "downloaded".
func scenePNG(w, h int) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, scene(w, h)); err != nil {
		return nil // can't happen for an in-memory RGBA image
	}
	return b.Bytes()
}

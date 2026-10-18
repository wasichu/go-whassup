package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/wa"
)

func TestInlineQRImage(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24}) // too small for the text QR
	h.key("down")
	h.key("enter")
	h.settle()

	v := h.view()
	if !strings.Contains(v, string(rune(0x10EEEE))) || strings.Contains(v, "doesn't fit") {
		t.Fatalf("inline image placeholders should show in 80x24:\n%s", v)
	}
	if !h.rawContains("a=T,U=1") {
		t.Fatal("image was not transmitted")
	}

	h.key("esc") // cancel: image freed
	if !h.rawContains("a=d,d=I") || h.model().login.image {
		t.Error("image not deleted on cancel")
	}
}

func TestInlineImagesRespectSetting(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	cfg := h.model().deps.Config
	cfg.UI.InlineImages = false
	m := h.model()
	m.deps.Config = cfg
	h.m = m
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if strings.Contains(h.view(), string(rune(0x10EEEE))) || h.rawContains("a=T") {
		t.Error("inline_images = false should draw text blocks")
	}
	if !strings.Contains(h.view(), "▀") {
		t.Error("text-block QR missing")
	}
}

func TestInlineImagesNeedTrueColor(t *testing.T) {
	h := newHarness(t, false)
	h.withGraphics()
	h.send(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if h.rawContains("a=T") || !strings.Contains(h.view(), "▀") {
		t.Error("without true color, the QR should fall back to text blocks")
	}
}

func TestTmuxHintAndWrapping(t *testing.T) {
	h := newHarness(t, false)
	m := h.model()
	m.deps.Graphics = termcaps.Graphics{Hint: "enable tmux passthrough"}
	h.m = m
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 140, Height: 45})
	h.key("down")
	h.key("enter")
	h.settle()
	if !strings.Contains(h.view(), "Tip: enable tmux passthrough") || !strings.Contains(h.view(), "▀") {
		t.Errorf("hint with text QR expected:\n%s", h.view())
	}

	h2 := newHarness(t, false)
	h2.withGraphics()
	m2 := h2.model()
	m2.deps.Graphics.Tmux = true
	h2.m = m2
	h2.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h2.key("down")
	h2.key("enter")
	h2.settle()
	if !h2.rawContains("\x1bPtmux;") {
		t.Error("upload not wrapped for tmux")
	}
}

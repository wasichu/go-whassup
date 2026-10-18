package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

func TestUnpairedStartsOnLoginScreen(t *testing.T) {
	h := newHarness(t, false)
	v := h.view()
	for _, want := range []string{"Link go-whassup to WhatsApp", "phone number code", "QR code", "not linked"} {
		if !strings.Contains(v, want) {
			t.Errorf("login view missing %q:\n%s", want, v)
		}
	}
}

func TestPhonePairingFlow(t *testing.T) {
	h := newHarness(t, false)
	h.client.PairCode = "ABCD-EFGH"
	h.key("enter") // phone code option
	h.key("+1 555 123 4567")
	h.key("enter")
	if got := h.client.PairPhone(); got != "+1 555 123 4567" {
		t.Fatalf("paired phone = %q", got)
	}
	if !strings.Contains(h.view(), "A B C D - E F G H") {
		t.Fatalf("code not shown:\n%s", h.view())
	}

	h.client.Emit(wa.PairSuccess{JID: ada})
	h.settle()
	if !strings.Contains(h.view(), "Linked as "+ada) {
		t.Errorf("linked state missing:\n%s", h.view())
	}
	h.client.Emit(wa.Connected{})
	h.settle()
	if h.model().screen != screenMain || !strings.Contains(h.view(), "● online") {
		t.Errorf("should return to main, online:\n%s", h.view())
	}
}

func TestPhonePairingRejectsBadNumber(t *testing.T) {
	h := newHarness(t, false)
	h.key("enter")
	h.key("0555")
	h.key("enter")
	if h.client.PairPhone() != "" {
		t.Error("Pair called with an invalid number")
	}
	if !strings.Contains(h.view(), "international format") {
		t.Errorf("validation error missing:\n%s", h.view())
	}
}

func TestQRPairingAndCancel(t *testing.T) {
	h := newHarness(t, false)
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 45})
	h.key("down")
	h.key("enter") // QR option
	h.settle()
	if !strings.Contains(h.view(), "▀") || !strings.Contains(h.view(), "scan") {
		t.Fatalf("QR not shown:\n%s", h.view())
	}

	h.key("esc") // cancels pairing, stays on the screen
	if h.client.Cancelled() != 1 || h.model().screen != screenLogin || h.model().login.step != loginChoose {
		t.Errorf("first esc: cancelled=%d screen=%v step=%v", h.client.Cancelled(), h.model().screen, h.model().login.step)
	}
	// A late QR from the cancelled pairing must not reopen the QR view.
	h.client.Emit(wa.PairQRCode{Code: sampleQR})
	h.settle()
	if h.model().login.step != loginChoose {
		t.Error("late QR event changed the step")
	}
}

func TestQRLayoutFitsCommonWindows(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantFit       bool
		wantShort     string
	}{
		{"wide but short (the reported 137x37)", 137, 37, true, ""},
		{"narrow, just tall enough", 80, 37, true, ""},
		{"standard 80x24", 80, 24, false, "more rows"},
		{"too narrow", 60, 50, false, "more columns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, false)
			h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
			h.send(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			h.key("down")
			h.key("enter")
			h.settle()
			v := h.view()
			if got := strings.Contains(v, "▀"); got != tt.wantFit {
				t.Fatalf("QR shown = %v, want %v:\n%s", got, tt.wantFit, v)
			}
			if tt.wantShort != "" && !strings.Contains(v, tt.wantShort) {
				t.Errorf("hint should mention %q:\n%s", tt.wantShort, v)
			}
			if lines := strings.Count(v, "\n") + 1; lines > tt.height {
				t.Errorf("view is %d lines in a %d-line terminal", lines, tt.height)
			}
		})
	}
}

func TestQRAppearsAfterResize(t *testing.T) {
	h := newHarness(t, false)
	h.client.QREvents = []wa.Event{wa.PairQRCode{Code: sampleQR}}
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.key("down")
	h.key("enter")
	h.settle()
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40}) // user zooms out
	if !strings.Contains(h.view(), "▀") {
		t.Errorf("QR should appear once it fits:\n%s", h.view())
	}
}

func TestPairFailedAndRetry(t *testing.T) {
	h := newHarness(t, false)
	h.client.PairCode = "ABCD-EFGH"
	h.key("enter")
	h.key("15551234567")
	h.key("enter")
	h.client.Emit(wa.PairFailed{Reason: "timed out"})
	h.settle()
	if !strings.Contains(h.view(), "Pairing failed: timed out") {
		t.Fatalf("failure not shown:\n%s", h.view())
	}
	h.key("enter")
	if h.model().login.step != loginChoose {
		t.Errorf("enter after failure should return to choose, step = %v", h.model().login.step)
	}
}

func TestRelinkFromMainScreen(t *testing.T) {
	h := newHarness(t, true)
	h.client.PhoneUnlinked = true
	h.key("L")
	if h.model().screen != screenLogin || !strings.Contains(h.view(), "Re-link this device") {
		t.Fatalf("L should open login with re-link:\n%s", h.view())
	}
	h.key("enter")
	if !strings.Contains(h.view(), "Re-link now? (y/n)") {
		t.Fatalf("confirmation missing:\n%s", h.view())
	}
	h.key("y")
	if h.client.IsPaired() {
		t.Fatal("Logout not called")
	}
	v := h.view()
	if !strings.Contains(v, "Unlinked. Your stored messages are kept.") || !strings.Contains(v, "Link with a QR code") {
		t.Errorf("after unlink should offer pairing:\n%s", v)
	}
	h.key("esc") // leave without pairing
	if h.model().screen != screenMain {
		t.Error("esc from choose should return to main")
	}
}

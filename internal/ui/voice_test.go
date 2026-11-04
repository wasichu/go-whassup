package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/voice"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

type fakeVoiceAudio struct {
	mu            sync.Mutex
	starts, plays int
	offsets       []time.Duration
	recordErr     error
}

func (a *fakeVoiceAudio) Record(ctx context.Context) (voice.Clip, error) {
	a.mu.Lock()
	a.starts++
	a.mu.Unlock()
	<-ctx.Done()
	return voice.Clip{Data: []byte("fake Ogg Opus recording"), Seconds: 7, Waveform: make([]byte, 64)}, a.recordErr
}

func (a *fakeVoiceAudio) Play(ctx context.Context, _ []byte, offset time.Duration) error {
	a.mu.Lock()
	a.plays++
	a.offsets = append(a.offsets, offset)
	a.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (a *fakeVoiceAudio) counts() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts, a.plays
}

func newVoiceHarness(t *testing.T) (*harness, *fakeVoiceAudio) {
	t.Helper()
	h := newHarness(t, true)
	audio := &fakeVoiceAudio{}
	m := h.model()
	m.deps.Voice = audio
	h.m = m
	h.key("enter")
	return h, audio
}

func TestVoiceRecordPreviewSend(t *testing.T) {
	h, audio := newVoiceHarness(t)
	h.key("ctrl+g")
	h.waitFor("recording started", func() bool { n, _ := audio.counts(); return n == 1 })
	if h.model().screen != screenVoice || !strings.Contains(h.view(), "Recording") {
		t.Fatalf("recording screen missing: %s", h.view())
	}
	h.key("enter")
	h.waitFor("recording ready", func() bool { return len(h.model().voice.clip.Data) > 0 })
	if len(h.client.SentVoices()) != 0 {
		t.Fatal("stopping recording sent it")
	}
	h.key("space")
	h.waitFor("preview started", func() bool { _, n := audio.counts(); return n == 1 })
	h.key("space")
	if h.model().voice.playing {
		t.Fatal("preview did not pause")
	}
	h.key("enter")
	h.waitFor("voice message sent", func() bool { return h.model().screen == screenMain })
	voices := h.client.SentVoices()
	if len(voices) != 1 || voices[0].Chat != ada || voices[0].Seconds != 7 {
		t.Fatalf("sent voices=%+v", voices)
	}
	if len(h.model().voice.clip.Data) != 0 {
		t.Fatal("sent draft retained in UI")
	}
}

func TestVoiceDiscardCancelsAndIgnoresLateResult(t *testing.T) {
	h, audio := newVoiceHarness(t)
	h.key("ctrl+g")
	h.waitFor("recording started", func() bool { n, _ := audio.counts(); return n == 1 })
	old := h.model().voice.token
	h.key("esc")
	h.settle()
	if h.model().screen != screenMain || len(h.client.SentVoices()) != 0 {
		t.Fatal("discard did not leave without sending")
	}
	h.key("ctrl+g")
	h.send(voiceReadyMsg{token: old, take: 1, clip: voice.Clip{Data: []byte("stale")}})
	if !h.model().voice.recording || len(h.model().voice.clip.Data) != 0 {
		t.Fatal("stale recording replaced a new one")
	}
	h.key("esc")
}

func TestVoiceFailedSendKeepsDraftForExplicitRetry(t *testing.T) {
	h, _ := newVoiceHarness(t)
	h.key("ctrl+g")
	h.key("enter")
	h.waitFor("recording ready", func() bool { return len(h.model().voice.clip.Data) > 0 })
	h.client.SendErr = errors.New("offline")
	h.key("enter")
	h.settle()
	if h.model().screen != screenVoice || len(h.model().voice.clip.Data) == 0 || h.model().voice.sending {
		t.Fatal("failed send lost the draft")
	}
	h.client.SendErr = nil
	h.key("enter")
	h.waitFor("retry sent", func() bool { return h.model().screen == screenMain })
	if len(h.client.SentVoices()) != 1 {
		t.Fatal("retry sent more than once")
	}
}

func TestReceivedVoiceUsesInAppPlayback(t *testing.T) {
	h, audio := newVoiceHarness(t)
	h.client.DownloadData = []byte("voice bytes for the fake audio device")
	_, err := h.st.SaveMessages(t.Context(), []wa.Message{{ID: "voice1", Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: "audio", Media: watest.VoiceMedia(361)}})
	if err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.key("ctrl+o")
	h.key("enter")
	h.waitFor("voice playback", func() bool { _, n := audio.counts(); return n == 1 })
	if !strings.Contains(h.view(), "6:01") || len(h.opened) != 0 {
		t.Fatalf("voice did not use in-app playback: %s", h.view())
	}
	h.key("space")
	if h.model().voice.playing {
		t.Fatal("received playback did not pause")
	}
	h.key("esc")
	if h.model().screen != screenPicker {
		t.Fatal("back did not return to attachments")
	}
}

func TestPickedVoiceOpensPlayerAndReturnsToSelection(t *testing.T) {
	h, audio := newVoiceHarness(t)
	h.client.DownloadData = []byte("voice bytes for the fake audio device")
	_, err := h.st.SaveMessages(t.Context(), []wa.Message{{ID: "picked-voice", Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: "audio", Media: watest.VoiceMedia(8)}})
	if err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.key("my unsent draft")
	h.key("ctrl+r")
	if h.model().conv.picked != "picked-voice" {
		t.Fatal("did not select voice note")
	}
	h.key("enter")
	h.waitFor("selected voice playback", func() bool { _, n := audio.counts(); return n == 1 })
	if h.model().screen != screenVoice {
		t.Fatal("did not open voice player")
	}
	h.key("esc")
	m := h.model()
	if m.screen != screenMain || !m.conv.picking || m.conv.picked != "picked-voice" {
		t.Fatal("did not return to selected message")
	}
	if m.conv.input.Value() != "my unsent draft" || len(h.client.Sent) != 0 {
		t.Fatal("opening voice changed or sent the draft")
	}
}

func TestPickedAttachmentOpensAndTextEnterDoesNotSend(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	h.seedAttachment("picked-doc", "report.pdf")
	h.key("enter")
	h.key("unsent")
	h.key("ctrl+r")
	h.key("enter")
	h.settle()
	if len(h.opened) != 1 || h.model().screen != screenMain || !h.model().conv.picking {
		t.Fatal("selected attachment did not open in place")
	}
	if h.model().conv.input.Value() != "unsent" {
		t.Fatal("opening attachment changed draft")
	}
	h.key("up") // select the preceding text message
	h.key("enter")
	h.settle()
	if len(h.opened) != 1 || len(h.client.Sent) != 0 || h.model().conv.input.Value() != "unsent" {
		t.Fatal("Enter on selected text opened/sent something")
	}
}

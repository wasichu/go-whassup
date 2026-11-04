package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/thesyncim/gopus/container/ogg"
)

func tone(seconds float64) []int16 {
	pcm := make([]int16, int(seconds*SampleRate))
	for i := range pcm {
		pcm[i] = int16(12000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}
	return pcm
}

func recorded(t *testing.T, pcm []int16) Clip {
	t.Helper()
	r, err := newRecording()
	if err != nil {
		t.Fatal(err)
	}
	for len(pcm) > 0 {
		n := min(731, len(pcm)) // deliberately straddle codec frames
		if err := r.write(pcm[:n]); err != nil {
			t.Fatal(err)
		}
		pcm = pcm[n:]
	}
	clip, err := r.finish()
	if err != nil {
		t.Fatal(err)
	}
	return clip
}

func TestVoiceOpusRoundTrip(t *testing.T) {
	clip := recorded(t, tone(0.6))
	if clip.Seconds != 1 || len(clip.Waveform) != 64 || !bytes.HasPrefix(clip.Data, []byte("OggS")) {
		t.Fatalf("invalid voice clip: duration=%d, waveform=%d", clip.Seconds, len(clip.Waveform))
	}
	r, channels, err := newDecoder(t.Context(), clip.Data)
	if err != nil {
		t.Fatal(err)
	}
	if channels != 1 {
		t.Fatalf("channels=%d", channels)
	}
	pcm, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	// The 16kHz recording is decoded at 48kHz, with at most one padded frame.
	if len(pcm) < 2*48000*6/10 || len(pcm) > 2*48000*64/100 {
		t.Fatalf("decoded bytes=%d", len(pcm))
	}
	var energy float64
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i:]))
		energy += float64(s) * float64(s)
	}
	if math.Sqrt(energy/float64(len(pcm)/2)) < 1000 {
		t.Fatal("decoded tone lost its audio")
	}
}

func TestDecodeIndependentOpusFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/tone.ogg")
	if err != nil {
		t.Fatal(err)
	}
	r, channels, err := newDecoder(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if channels != 1 || len(pcm) != 48000 {
		t.Fatalf("independent 0.5s fixture: channels=%d, bytes=%d", channels, len(pcm))
	}
}

func TestPhoneStyleVoiceWithoutEndMarker(t *testing.T) {
	data, err := os.ReadFile("testdata/tone.ogg")
	if err != nil {
		t.Fatal(err)
	}
	var phoneStyle bytes.Buffer
	for len(data) > 0 {
		page, n, err := ogg.ParsePage(data)
		if err != nil {
			t.Fatal(err)
		}
		page.HeaderType &^= ogg.PageFlagEOS
		phoneStyle.Write(page.Encode()) // recompute checksums after removing the flag
		data = data[n:]
	}
	r, channels, err := newDecoder(t.Context(), phoneStyle.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if channels != 1 || len(pcm) != 48000 {
		t.Fatalf("phone-style voice: channels=%d, bytes=%d", channels, len(pcm))
	}
	if _, _, err := newDecoder(t.Context(), phoneStyle.Bytes()[:phoneStyle.Len()-1]); err == nil {
		t.Fatal("accepted an incomplete page body")
	}
}

func TestMissingVoicePageStillRejected(t *testing.T) {
	data := recorded(t, tone(0.6)).Data
	var missing bytes.Buffer
	for len(data) > 0 {
		page, n, err := ogg.ParsePage(data)
		if err != nil {
			t.Fatal(err)
		}
		if page.PageSequence != 3 {
			missing.Write(data[:n])
		}
		data = data[n:]
	}
	if _, _, err := newDecoder(t.Context(), missing.Bytes()); err == nil {
		t.Fatal("accepted voice note with a missing page")
	}
}

func TestMalformedAndCancelledVoice(t *testing.T) {
	if _, _, err := newDecoder(t.Context(), []byte("not audio")); err == nil {
		t.Fatal("accepted malformed audio")
	}
	clip := recorded(t, tone(0.4))
	corrupt := bytes.Clone(clip.Data)
	corrupt[len(corrupt)/2] ^= 1
	r, _, err := newDecoder(t.Context(), corrupt)
	if err == nil {
		_, err = io.Copy(io.Discard, r)
	}
	if err == nil {
		t.Fatal("accepted a corrupt Ogg stream")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, _, err = newDecoder(ctx, clip.Data)
	if errors.Is(err, context.Canceled) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled decode: %v", err)
	}
}

func TestPlaybackLongerThanFiveMinutes(t *testing.T) {
	// Repeating a valid silence packet keeps the fixture tiny and the test offline.
	clip := recorded(t, make([]int16, SampleRate))
	source, err := ogg.NewReader(bytes.NewReader(clip.Data))
	if err != nil {
		t.Fatal(err)
	}
	packet, _, err := source.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := ogg.NewWriter(&out, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 15051 {
		if err := w.WritePacket(packet, 960); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, _, err := newDecoder(t.Context(), out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 5*60*48000*2 {
		t.Fatalf("long voice note was truncated: %d bytes", n)
	}
}

func TestRecordingLongerThanFiveMinutes(t *testing.T) {
	r, err := newRecording()
	if err != nil {
		t.Fatal(err)
	}
	second := make([]int16, SampleRate)
	for range 301 {
		if err := r.write(second); err != nil {
			t.Fatal(err)
		}
	}
	clip, err := r.finish()
	if err != nil {
		t.Fatal(err)
	}
	if clip.Seconds != 301 || len(clip.Waveform) != 64 {
		t.Fatalf("long recording: duration=%d waveform=%d", clip.Seconds, len(clip.Waveform))
	}
}

type fakeDevice struct {
	pcm     []int16
	err     error
	played  int
	started chan struct{}
}

func (d *fakeDevice) capture(ctx context.Context, consume func([]int16) error) error {
	if d.err != nil {
		return d.err
	}
	if err := consume(d.pcm); err != nil {
		return err
	}
	if d.started != nil {
		close(d.started)
	}
	<-ctx.Done()
	return nil
}

func (d *fakeDevice) play(_ context.Context, r io.Reader, _ int) error {
	n, err := io.Copy(io.Discard, r)
	d.played = int(n)
	return err
}

func TestRecordStopAndPlaybackOffset(t *testing.T) {
	d := &fakeDevice{pcm: tone(1), started: make(chan struct{})}
	e := &Engine{device: d}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan Clip, 1)
	failure := make(chan error, 1)
	go func() { clip, err := e.Record(ctx); result <- clip; failure <- err }()
	<-d.started
	cancel()
	clip, err := <-result, <-failure
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Play(t.Context(), clip.Data, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if d.played < 48000 || d.played > 52000 {
		t.Fatalf("offset playback bytes=%d", d.played)
	}
	d.err = errors.New("microphone permission denied")
	if _, err := e.Record(t.Context()); !errors.Is(err, d.err) {
		t.Fatalf("capture error=%v", err)
	}
}

func TestCancelledRecordingNeverOpensDevice(t *testing.T) {
	d := &fakeDevice{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Engine{device: d}).Record(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recording: %v", err)
	}
	select {
	case <-d.started:
		t.Fatal("cancelled recording opened microphone")
	default:
	}
}

// Package voice records and plays Ogg Opus voice notes without cgo or helper programs.
package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const SampleRate = 16000

// MaxEncodedSize matches the application's attachment send limit, not a WhatsApp duration limit.
const MaxEncodedSize = 100 << 20

var errRecordingFull = errors.New("voice recording reached the attachment size limit")

// Clip is an unsent voice note. Drafts stay in memory until explicitly sent.
type Clip struct {
	Data     []byte
	Seconds  uint32
	Waveform []byte
}

type device interface {
	capture(context.Context, func([]int16) error) error
	play(context.Context, io.Reader, int) error
}

// Engine owns the audio backend. Each operation opens and closes its own device.
type Engine struct{ device device }

func New() *Engine { return &Engine{device: nativeDevice{}} }

// Record encodes while capturing. Cancellation stops recording and returns the
// completed draft; it never sends it. Only compressed audio is retained.
func (e *Engine) Record(ctx context.Context) (Clip, error) {
	if err := ctx.Err(); err != nil {
		return Clip{}, err
	}
	r, err := newRecording()
	if err != nil {
		return Clip{}, err
	}
	if err := e.device.capture(ctx, r.write); err != nil && !errors.Is(err, errRecordingFull) {
		return Clip{}, fmt.Errorf("recording voice note: %w", err)
	}
	return r.finish()
}

// Play decodes incrementally from offset. Cancelling ctx stops playback.
func (e *Engine) Play(ctx context.Context, data []byte, offset time.Duration) error {
	r, channels, err := newDecoder(ctx, data)
	if err != nil {
		return err
	}
	skip := int64(max(offset, 0).Seconds()*48000) * int64(channels) * 2
	if _, err := io.CopyN(io.Discard, r, skip); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("seeking voice note: %w", err)
	}
	return e.device.play(ctx, r, channels)
}

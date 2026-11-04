package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jfreymuth/pulse"
)

type nativeDevice struct{}

func (nativeDevice) capture(ctx context.Context, consume func([]int16) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := pulse.NewClient(pulse.ClientApplicationName("go-whassup"), pulse.ClientTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("connecting to desktop audio (PulseAudio or PipeWire-Pulse): %w", err)
	}
	defer c.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	failed := make(chan error, 1)
	write := pulse.Int16Writer(func(p []int16) (int, error) {
		if err := consume(p); err != nil {
			select {
			case failed <- err:
			default:
			}
			return 0, err
		}
		return len(p), nil
	})
	r, err := c.NewRecord(write, pulse.RecordMono, pulse.RecordSampleRate(SampleRate), pulse.RecordLatency(0.1))
	if err != nil {
		return fmt.Errorf("opening microphone: %w", err)
	}
	defer r.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.Start()
	select {
	case <-ctx.Done():
	case err = <-failed:
	}
	r.Stop()
	if err != nil {
		return err
	}
	if err := r.Error(); err != nil {
		return fmt.Errorf("capturing microphone audio: %w", err)
	}
	return nil
}

type pulseReader struct{ io.Reader }

func (r pulseReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		return n, nil
	} // pulse drops a final buffer accompanied by EOF
	if errors.Is(err, io.EOF) {
		return 0, pulse.EndOfData
	}
	return n, err
}

func (nativeDevice) play(ctx context.Context, reader io.Reader, channels int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := pulse.NewClient(pulse.ClientApplicationName("go-whassup"), pulse.ClientTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("connecting to desktop audio (PulseAudio or PipeWire-Pulse): %w", err)
	}
	defer c.Close()
	channelOption := pulse.PlaybackMono
	if channels == 2 {
		channelOption = pulse.PlaybackStereo
	}
	var format pulse.Int16Reader
	p, err := c.NewPlayback(pulse.NewReader(pulseReader{reader}, format.Format()), channelOption, pulse.PlaybackSampleRate(48000), pulse.PlaybackLatency(0.1))
	if err != nil {
		return fmt.Errorf("opening speaker: %w", err)
	}
	defer p.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	p.Start()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			p.Stop()
			return ctx.Err()
		case <-tick.C:
			if !p.Running() {
				// The server may still have a short tail queued after our reader reaches EOF.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(150 * time.Millisecond):
				}
				return p.Error()
			}
		}
	}
}

package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const frameSamples = SampleRate / 50

type recording struct {
	mu        sync.Mutex
	enc       *gopus.Encoder
	container *ogg.Writer
	out       bytes.Buffer
	frame     [frameSamples]int16
	packet    [4000]byte
	used      int
	samples   uint64
	envelope  envelope
}

func newRecording() (*recording, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		return nil, fmt.Errorf("creating Opus encoder: %w", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		return nil, fmt.Errorf("setting voice bitrate: %w", err)
	}
	r := &recording{enc: enc, envelope: envelope{width: frameSamples}}
	r.container, err = ogg.NewWriterWithConfig(&r.out, ogg.WriterConfig{SampleRate: SampleRate, Channels: 1, PreSkip: uint16(enc.Lookahead() * 3)})
	if err != nil {
		return nil, fmt.Errorf("creating voice container: %w", err)
	}
	return r, nil
}

func (r *recording) write(p []int16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(p) > 0 {
		if r.out.Len() >= MaxEncodedSize-65536 {
			return errRecordingFull
		}
		n := copy(r.frame[r.used:], p)
		r.envelope.add(p[:n])
		r.samples += uint64(n)
		r.used += n
		p = p[n:]
		if r.used == frameSamples {
			if err := r.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *recording) flush() error {
	n, err := r.enc.EncodeInt16(r.frame[:], r.packet[:])
	if err != nil {
		return fmt.Errorf("encoding voice note: %w", err)
	}
	if err := r.container.WritePacket(r.packet[:n], 960); err != nil {
		return fmt.Errorf("writing voice note: %w", err)
	}
	r.used = 0
	clear(r.frame[:])
	return nil
}

func (r *recording) finish() (Clip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.samples < SampleRate/3 {
		if r.samples == 0 {
			return Clip{}, errors.New("no microphone audio captured; check microphone permission and the default input device")
		}
		return Clip{}, errors.New("record at least a third of a second")
	}
	// Padding flushes lookahead and the partial final frame.
	padding := r.enc.Lookahead()
	for padding > 0 || r.used > 0 {
		n := min(frameSamples-r.used, padding)
		padding -= n
		r.used += n
		if r.used == frameSamples || padding == 0 {
			if err := r.flush(); err != nil {
				return Clip{}, err
			}
		}
	}
	if err := r.container.Close(); err != nil {
		return Clip{}, fmt.Errorf("closing voice container: %w", err)
	}
	return Clip{Data: r.out.Bytes(), Seconds: uint32((r.samples + SampleRate - 1) / SampleRate), Waveform: r.envelope.waveform()}, nil
}

// envelope keeps a bounded peak summary, merging buckets as a recording grows.
type envelope struct {
	peaks               [64]uint16
	filled, width, used int
}

func (e *envelope) add(p []int16) {
	for _, sample := range p {
		if e.filled == len(e.peaks) {
			for i := range len(e.peaks) / 2 {
				e.peaks[i] = max(e.peaks[2*i], e.peaks[2*i+1])
			}
			clear(e.peaks[len(e.peaks)/2:])
			e.filled, e.width = len(e.peaks)/2, e.width*2
		}
		e.peaks[e.filled] = max(e.peaks[e.filled], uint16(absSample(sample)))
		e.used++
		if e.used == e.width {
			e.filled++
			e.used = 0
		}
	}
}

func absSample(s int16) int {
	if s < 0 {
		return -int(s)
	}
	return int(s)
}

func (e *envelope) waveform() []byte {
	wave := make([]byte, 64)
	count := e.filled
	if e.used > 0 {
		count++
	}
	if count == 0 {
		return wave
	}
	for i := range wave {
		wave[i] = byte(uint32(e.peaks[min(i*count/64, count-1)]) * 100 / 32768)
	}
	return wave
}

type opusReader struct {
	ctx      context.Context
	source   *ogg.Reader
	decoder  *gopus.Decoder
	channels int
	skip     int
	gain     float64
	decoded  uint64
	end      uint64
	pcm      []int16
	bytes    []byte
	pending  []byte
}

func newDecoder(ctx context.Context, data []byte) (*opusReader, int, error) {
	if len(data) > MaxEncodedSize {
		return nil, 0, errors.New("voice note exceeds the attachment size limit")
	}
	end, err := validateOgg(ctx, data)
	if err != nil {
		return nil, 0, err
	}
	r, err := ogg.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("reading Ogg Opus voice note: %w", err)
	}
	channels := int(r.Channels())
	if channels < 1 || channels > 2 || r.Header.MappingFamily != 0 {
		return nil, 0, errors.New("unsupported voice-note channel layout")
	}
	d, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return nil, 0, fmt.Errorf("creating voice decoder: %w", err)
	}
	return &opusReader{ctx: ctx, source: r, decoder: d, channels: channels, skip: int(r.PreSkip()) * channels, end: end,
		gain: math.Pow(10, float64(r.Header.OutputGain)/(256*20)), pcm: make([]int16, 5760*channels), bytes: make([]byte, 11520*channels)}, channels, nil
}

// Validate pages before decoding: this codec release's streaming reader can
// report EOF for a corrupt/truncated page. ParsePage preserves the real error
// and bounds each page using Ogg's byte-sized lacing table.
func validateOgg(ctx context.Context, data []byte) (uint64, error) {
	var stream oggValidation
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		page, n, err := ogg.ParsePage(data)
		if err != nil {
			return 0, fmt.Errorf("invalid Ogg voice note: %w", err)
		}
		if n <= 0 {
			return 0, errors.New("invalid empty Ogg page")
		}
		if err := stream.observe(page); err != nil {
			return 0, err
		}
		data = data[n:]
	}
	// Phone-recorded WhatsApp notes can end at a complete audio page without
	// an EOS flag. A missing marker alone does not mean the download is cut off.
	if !stream.seen || !stream.complete || stream.end == 0 {
		return 0, errors.New("unfinished Ogg voice note")
	}
	return stream.end, nil
}

type oggValidation struct {
	serial, next        uint32
	seen, complete, eos bool
	end                 uint64
}

func (v *oggValidation) observe(page *ogg.Page) error {
	if !v.seen {
		v.serial, v.next, v.seen = page.SerialNumber, page.PageSequence, true
	}
	if page.SerialNumber != v.serial || v.eos {
		return nil
	}
	if page.PageSequence != v.next {
		return errors.New("missing or out-of-order Ogg voice page")
	}
	v.next++
	if page.GranulePos != math.MaxUint64 {
		v.end = page.GranulePos
	}
	v.complete = len(page.Segments) == 0 || page.Segments[len(page.Segments)-1] < 255
	v.eos = page.IsEOS()
	return nil
}

func (r *opusReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.decoded >= r.end {
			return 0, io.EOF
		}
		packet, _, err := r.source.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, fmt.Errorf("reading voice packet: %w", err)
		}
		n, err := r.decoder.DecodeInt16(packet, r.pcm)
		if err != nil {
			return 0, fmt.Errorf("decoding voice packet: %w", err)
		}
		// Use the final complete page granule; the packet reader interpolates page
		// timestamps and can otherwise trim every packet on a multi-packet page.
		frames := min(uint64(n), r.end-r.decoded)
		r.decoded += uint64(n)
		samples := r.pcm[:int(frames)*r.channels]
		skip := min(r.skip, len(samples))
		r.skip -= skip
		samples = samples[skip:]
		for i, s := range samples {
			value := int16(max(-32768, min(32767, float64(s)*r.gain)))
			binary.LittleEndian.PutUint16(r.bytes[i*2:], uint16(value))
		}
		r.pending = r.bytes[:len(samples)*2]
	}
	n := copy(dst, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

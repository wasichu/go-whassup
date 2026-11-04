package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Core Audio's AudioQueue ABI, from AudioToolbox/AudioQueue.h. All framework
// allocations remain owned by AudioQueue; no Go pointers are passed as user data.
type audioFormat struct {
	Rate                                                                                float64
	ID, Flags, BytesPerPacket, FramesPerPacket, BytesPerFrame, Channels, Bits, Reserved uint32
}

type audioBuffer struct {
	Capacity       uint32
	Data           unsafe.Pointer
	Size           uint32
	User           unsafe.Pointer
	PacketCapacity uint32
	Packets        unsafe.Pointer
	PacketCount    uint32
}

type audioAPI struct {
	newInput, newOutput func(*audioFormat, uintptr, uintptr, uintptr, uintptr, uint32, *uintptr) int32
	allocate            func(uintptr, uint32, **audioBuffer) int32
	enqueue             func(uintptr, *audioBuffer, uint32, uintptr) int32
	start               func(uintptr, uintptr) int32
	stop, dispose       func(uintptr, uint8) int32
}

var (
	audioOnce                     sync.Once
	audio                         audioAPI
	audioErr                      error
	inputCallback, outputCallback uintptr
	queues                        sync.Map
	queueID                       atomic.Uint64
)

func loadAudio() error {
	audioOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			audioErr = fmt.Errorf("loading Core Audio: %w", err)
			return
		}
		// The system framework and callbacks live for the process lifetime.
		purego.RegisterLibFunc(&audio.newInput, lib, "AudioQueueNewInput")
		purego.RegisterLibFunc(&audio.newOutput, lib, "AudioQueueNewOutput")
		purego.RegisterLibFunc(&audio.allocate, lib, "AudioQueueAllocateBuffer")
		purego.RegisterLibFunc(&audio.enqueue, lib, "AudioQueueEnqueueBuffer")
		purego.RegisterLibFunc(&audio.start, lib, "AudioQueueStart")
		purego.RegisterLibFunc(&audio.stop, lib, "AudioQueueStop")
		purego.RegisterLibFunc(&audio.dispose, lib, "AudioQueueDispose")
		inputCallback = purego.NewCallback(inputReady)
		outputCallback = purego.NewCallback(outputReady)
	})
	return audioErr
}

type nativeDevice struct{}

type queueState struct {
	mu     sync.Mutex
	record func([]int16) error
	reader io.Reader
	eof    bool
	queued int
	done   chan struct{}
	once   sync.Once
	err    error
}

func (s *queueState) finish(err error) { s.once.Do(func() { s.err = err; close(s.done) }) }

func (nativeDevice) capture(ctx context.Context, consume func([]int16) error) error {
	return runQueue(ctx, &queueState{record: consume, done: make(chan struct{})}, SampleRate, 1)
}

func (nativeDevice) play(ctx context.Context, reader io.Reader, channels int) error {
	return runQueue(ctx, &queueState{reader: reader, done: make(chan struct{})}, 48000, channels)
}

func runQueue(ctx context.Context, s *queueState, rate, channels int) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := loadAudio(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	format := audioFormat{Rate: float64(rate), ID: 0x6c70636d, Flags: 12, BytesPerPacket: uint32(channels * 2), FramesPerPacket: 1, BytesPerFrame: uint32(channels * 2), Channels: uint32(channels), Bits: 16}
	id := uintptr(queueID.Add(1))
	queues.Store(id, s)
	defer queues.Delete(id)
	var q uintptr
	create, callback := audio.newOutput, outputCallback
	if s.record != nil {
		create, callback = audio.newInput, inputCallback
	}
	if code := create(&format, callback, id, 0, 0, 0, &q); code != 0 {
		return audioStatus("opening audio device (check microphone permission and default devices)", code)
	}
	defer func() { err = joinAudioError(err, "disposing audio device", audio.dispose(q, 1)) }()
	if err := prepareBuffers(q, s, uint32(rate*channels*2/10)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if code := audio.start(q, 0); code != 0 {
		return audioStatus("starting audio device", code)
	}
	select {
	case <-ctx.Done():
		if s.record == nil {
			err = ctx.Err()
		}
	case <-s.done:
		err = s.err
	}
	return joinAudioError(err, "stopping audio device", audio.stop(q, 1))
}

func prepareBuffers(q uintptr, s *queueState, size uint32) error {
	for range 3 {
		var b *audioBuffer
		if code := audio.allocate(q, size, &b); code != 0 {
			return audioStatus("allocating audio buffer", code)
		}
		if s.record != nil {
			if code := audio.enqueue(q, b, 0, 0); code != 0 {
				return audioStatus("queueing microphone buffer", code)
			}
		} else {
			fillOutput(q, s, b, false)
		}
	}
	return nil
}

func inputReady(id, q uintptr, b *audioBuffer, _ uintptr, _ uint32, _ uintptr) {
	v, ok := queues.Load(id)
	if !ok {
		return
	}
	s := v.(*queueState)
	if err := s.record(unsafe.Slice((*int16)(b.Data), int(b.Size)/2)); err != nil {
		s.finish(err)
		return
	}
	if code := audio.enqueue(q, b, 0, 0); code != 0 {
		s.finish(audioStatus("queueing microphone buffer", code))
	}
}

func outputReady(id, q uintptr, b *audioBuffer) {
	if v, ok := queues.Load(id); ok {
		fillOutput(q, v.(*queueState), b, true)
	}
}

func fillOutput(q uintptr, s *queueState, b *audioBuffer, returned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if returned {
		s.queued--
	}
	if s.eof {
		if s.queued == 0 {
			s.finish(nil)
		}
		return
	}
	n, err := io.ReadFull(s.reader, unsafe.Slice((*byte)(b.Data), int(b.Capacity)))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		s.finish(err)
		return
	}
	s.eof = err != nil
	if n == 0 {
		if s.queued == 0 {
			s.finish(nil)
		}
		return
	}
	b.Size = uint32(n)
	s.queued++
	if code := audio.enqueue(q, b, 0, 0); code != 0 {
		s.finish(audioStatus("queueing playback buffer", code))
	}
}

func audioStatus(what string, code int32) error {
	return fmt.Errorf("%s: Core Audio status %d", what, code)
}

func joinAudioError(err error, what string, code int32) error {
	if code == 0 {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w; %s: Core Audio status %d", err, what, code)
	}
	return audioStatus(what, code)
}

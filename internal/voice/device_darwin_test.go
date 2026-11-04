package voice

import (
	"testing"
	"unsafe"
)

// Loading symbols and creating callbacks does not open an audio device or ask
// for microphone access. Check the ABI on both macOS architectures in CI.
func TestCoreAudioBindings(t *testing.T) {
	if unsafe.Sizeof(audioFormat{}) != 40 || unsafe.Sizeof(audioBuffer{}) != 56 {
		t.Fatal("Core Audio ABI layout mismatch")
	}
	if err := loadAudio(); err != nil {
		t.Fatal(err)
	}
	if inputCallback == 0 || outputCallback == 0 || audio.newInput == nil || audio.newOutput == nil {
		t.Fatal("missing Core Audio bindings")
	}
}

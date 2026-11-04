package wa

import (
	"bytes"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestVoiceMessageUsesNativePTTAndRoundTrips(t *testing.T) {
	data := append([]byte("OggS......................."), []byte("OpusHead")...)
	wave := bytes.Repeat([]byte{50}, 64)
	msg, err := voiceMessage(data, 361, wave, func(got []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		if kind != whatsmeow.MediaAudio || !bytes.Equal(got, data) {
			t.Error("voice was not uploaded as audio")
		}
		return whatsmeow.UploadResponse{URL: "https://example.invalid/audio", DirectPath: "/audio", MediaKey: []byte("secret"), FileLength: uint64(len(data))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	audio := msg.GetAudioMessage()
	if !audio.GetPTT() || audio.GetSeconds() != 361 || audio.GetMimetype() != "audio/ogg; codecs=opus" || !bytes.Equal(audio.GetWaveform(), wave) {
		t.Fatalf("wrong voice metadata: %v", audio)
	}
	m := mediaOf(msg)
	stored, err := ParseMedia(m.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Voice || stored.Seconds != 361 {
		t.Fatalf("voice metadata lost after storage: %+v", stored)
	}
	if got := (Message{Kind: "audio", Media: stored}).Summary(); got != "[voice message] 6:01" {
		t.Fatalf("summary=%q", got)
	}
	bad, err := voiceMessage([]byte("bad"), 1, nil, func([]byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		t.Fatal("invalid voice uploaded")
		return whatsmeow.UploadResponse{}, nil
	})
	if err == nil || bad != nil {
		t.Fatal("accepted invalid audio")
	}
}

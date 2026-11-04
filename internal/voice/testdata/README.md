# Voice fixture

`tone.ogg` is a generated 0.5-second, mono 440 Hz sine wave, encoded by FFmpeg's
libopus encoder at 48 kHz and 24 kbit/s CBR. It contains no microphone or account
data. It verifies decoding independently of the Go encoder, including Ogg's
final granule trim and Opus pre-skip. Tests read the fixture without requiring
FFmpeg, an audio device, or network access.

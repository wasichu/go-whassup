# Voice messages

The feature includes both listening and recording/sending. WhatsApp voice notes
are audio messages with `PTT=true`, normally Ogg Opus. They are distinct from
audio files sent as documents. `SendFile` retains its existing behavior; the voice
screen calls `wa.Client.SendVoice` with encoded audio, duration, and waveform.

## Audio and dependency choices

`internal/voice` encodes mono 16 kHz speech at 24 kbit/s and decodes mono/stereo
Ogg Opus at 48 kHz. Encoder lookahead is recorded as Opus pre-skip and flushed at
the end. Incoming output gain and pre-skip are applied. Playback and recording
process small PCM chunks; only the compressed attachment is held in memory.

Three development choices preserve `CGO_ENABLED=0` and require no helper program:

- `github.com/thesyncim/gopus` provides pure-Go Opus encode/decode and Ogg framing.
  v0.1.2 supports the existing Go language minimum. Its streaming Ogg reader can
  turn a corrupt page into EOF, so we validate every page with `ogg.ParsePage`
  before decoding. Phone-recorded notes can omit Ogg's end-of-stream flag; a
  complete final audio page is accepted, with checksums and page ordering still
  checked. Partial pages and incomplete final packets are rejected. The final
  physical page's timestamp controls padding trim;
  packet-level interpolated timestamps do not. An independently encoded Opus
  fixture, malformed input, and cancellation are covered by offline tests.
- `github.com/ebitengine/purego` binds macOS's built-in Core Audio AudioQueue API.
  It requires no compiler, bundled dynamic library, or runtime installation. The
  AudioToolbox framework and callback trampolines live for the process lifetime;
  queues/buffers are created per operation and synchronously disposed on stop.
- `github.com/jfreymuth/pulse` speaks the PulseAudio protocol directly from Go on
  Linux, including PipeWire's PulseAudio compatibility service. The desktop audio
  service must already be available; a headless/SSH-only host can lack devices.

The macOS ABI is taken from the SDK's `AudioQueue.h` and
`AudioStreamBasicDescription`. CI tests symbol loading, callback construction,
and structure sizes without opening a microphone or speaker.

## Lifecycle and limits

Recording begins only on the user's shortcut. Stop prepares a draft; it never
sends. The user may preview, rerecord, discard, or explicitly send it. A failed
send retains the draft for a manual retry. Draft data and its waveform stay in
memory, never in the plaintext attachment cache or logs. Discard/quit releases
the draft; unsent drafts are not recovered after a restart.

Playback has pause/resume and stop. Pausing closes the audio operation and stores
an offset; resuming decodes/skips to that offset. This keeps the audio backend
simple and cancellable without retaining an uncompressed recording. Leaving the
voice screen stops audio, including screen changes caused by protocol events.
Generation tokens prevent results from a closed/replaced operation changing a
new recording or playback.

Selecting a message with `ctrl+r` and pressing Enter opens its attachment. Voice
notes open the in-app player and return to the same message selection on Esc;
other attachments use the ordinary system opener. The typed message draft and
selection are preserved, and Enter on a text/deleted message sends nothing.

No duration cutoff is imposed: five minutes is not a verified WhatsApp limit.
The compressed attachment limit is 100 MB, matching this app's existing send
limit, not a claim about WhatsApp's server limits. Recording stops at that size
and prepares the draft for review. The waveform uses a bounded peak summary.
Downloaded voice notes use the ordinary media cache and its existing encryption
policy; see `storage.md`. Listening does not send played receipts.

## Device verification

On 2026-11-04 the user confirmed playback of a phone-recorded note and successful
recording/sending from the TUI. The received note had complete Ogg pages without
an EOS marker; playback succeeded after fixing that validation assumption. No
private recording is stored as a test fixture.

Automated tests use generated audio, fake capture/playback, and `watest.Client`;
they do not access a real account, microphone, speakers, or network.

Additional device checks:

1. On macOS, grant the terminal microphone access, record speech, stop, preview,
   pause/resume, discard, and record again. Verify the microphone closes on Esc
   and Ctrl+C, including when permission is denied or no input is available.
2. Repeat against PulseAudio/PipeWire-Pulse on Linux, including an absent service.
3. Send a note to your own chat and play it on the phone. Check that WhatsApp
   shows a native voice note, its duration, waveform, and audible beginning/end.
4. Receive phone-recorded notes, including one longer than five minutes, and play
   them in-app. Test a failed/expired download and a failed send with draft retry.

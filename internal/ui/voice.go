package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/voice"
	"github.com/wasichu/go-whassup/internal/wa"
)

type voicePage struct {
	token, take, playSeq                             int
	chat                                             string
	received, loading, recording, preparing, sending bool
	backToPicker                                     bool
	clip                                             voice.Clip
	cancelRecord, cancelPlay, cancelLoad             context.CancelFunc
	started, playStarted                             time.Time
	playing                                          bool
	offset                                           time.Duration
}

type voiceTickMsg struct{ token int }
type voiceReadyMsg struct {
	token, take int
	clip        voice.Clip
	err         error
}
type voicePlayedMsg struct {
	token, seq int
	err        error
}
type voiceSentMsg struct {
	token int
	err   error
}

var (
	voiceRecordKey  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "record again"))
	voicePlayKey    = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "play/pause"))
	voiceStopKey    = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop playback"))
	voiceRecordHelp = []key.Binding{hint(keys.Select, "stop recording"), hint(keys.Back, "discard")}
	voiceDraftHelp  = []key.Binding{voicePlayKey, voiceStopKey, hint(keys.Send, "send voice message"), voiceRecordKey, hint(keys.Back, "discard")}
	voiceListenHelp = []key.Binding{voicePlayKey, voiceStopKey, keys.Back}
)

func (m Model) openVoiceRecorder() (Model, tea.Cmd) {
	if m.deps.Voice == nil || m.conv.chat.JID == "" {
		m.say("Voice recording is unavailable in this mode.")
		return m, nil
	}
	if m.conn != connOnline {
		m.say("Connect before recording a voice message.")
		return m, nil
	}
	m.voiceSeq++
	m.voice = voicePage{token: m.voiceSeq, chat: m.conv.chat.JID}
	m.screen = screenVoice
	cmd := m.voice.record(rootAPI{&m})
	return m, tea.Batch(cmd, voiceTick(m.voice.token))
}

func (m Model) openVoiceNote(item wa.Message) (Model, tea.Cmd) {
	if m.deps.Voice == nil || m.deps.Media == nil {
		m.say("Voice playback is unavailable in this mode.")
		return m, nil
	}
	m.voiceSeq++
	m.voice = voicePage{token: m.voiceSeq, chat: m.conv.chat.JID, received: true, loading: true, backToPicker: m.screen == screenPicker, clip: voice.Clip{Seconds: item.Media.Seconds}}
	m.screen = screenVoice
	ctx, cancel := context.WithCancel(m.ctx)
	m.voice.cancelLoad = cancel
	deps, token, chat := m.deps, m.voice.token, m.voice.chat
	load := func() tea.Msg {
		defer cancel()
		path, err := deps.Media.Fetch(ctx, deps.Client, chat, item.ID, *item.Media)
		clip := voice.Clip{Seconds: item.Media.Seconds}
		if err == nil {
			clip.Data, err = readVoiceFile(path)
		}
		return voiceReadyMsg{token: token, clip: clip, err: err}
	}
	return m, tea.Batch(load, voiceTick(token))
}

func readVoiceFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening voice note: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, voice.MaxEncodedSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading voice note: %w", err)
	}
	if len(data) > voice.MaxEncodedSize {
		return nil, errors.New("voice note exceeds the attachment size limit")
	}
	return data, nil
}

func voiceTick(token int) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return voiceTickMsg{token} })
}

func (p *voicePage) update(r rootAPI, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return p.key(r, msg)
	case voiceTickMsg:
		if msg.token == p.token {
			return false, voiceTick(p.token)
		}
	case voiceReadyMsg:
		return false, p.ready(r, msg)
	case voicePlayedMsg:
		if msg.token == p.token && msg.seq == p.playSeq {
			p.playing, p.offset, p.cancelPlay = false, 0, nil
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				r.setError("playing voice message", msg.err)
			}
		}
	case voiceSentMsg:
		if msg.token == p.token {
			p.sending = false
			if msg.err != nil {
				r.setError("sending voice message (draft kept; Enter retries)", msg.err)
				return false, nil
			}
			p.clip = voice.Clip{}
			r.notify("Voice message sent.")
			return true, nil
		}
	}
	return false, nil
}

func (p *voicePage) ready(r rootAPI, msg voiceReadyMsg) tea.Cmd {
	if msg.token != p.token || msg.take != p.take {
		return nil
	}
	p.loading, p.recording, p.preparing, p.cancelRecord = false, false, false, nil
	if msg.err != nil {
		if errors.Is(msg.err, wa.ErrMediaExpired) {
			r.notify("That voice note expired on WhatsApp; ask the sender to resend it.")
		} else {
			r.setError("preparing voice message", msg.err)
		}
		return nil
	}
	p.clip = msg.clip
	if p.received {
		return p.play(r)
	}
	r.notify("Recording ready. Space to listen; Enter to send; Esc to discard.")
	return nil
}

func (p *voicePage) key(r rootAPI, k tea.KeyPressMsg) (bool, tea.Cmd) {
	if p.sending {
		return false, nil
	} // never start a second send or lose a failed draft
	switch {
	case key.Matches(k, keys.Back):
		p.cancel()
		p.clip = voice.Clip{}
		r.clearNotice()
		if p.backToPicker {
			r.voiceBackToAttachments()
			return false, nil
		}
		return true, nil
	case p.recording && key.Matches(k, keys.Select):
		p.recording, p.preparing = false, true
		p.cancelRecord()
		return false, nil
	case !p.received && !p.preparing && key.Matches(k, voiceRecordKey):
		return false, p.record(r)
	case !p.recording && !p.preparing && key.Matches(k, voicePlayKey):
		return false, p.play(r)
	case key.Matches(k, voiceStopKey):
		p.stopPlayback(false)
	case !p.received && !p.recording && !p.preparing && key.Matches(k, keys.Send):
		return false, p.send(r)
	}
	return false, nil
}

func (p *voicePage) record(r rootAPI) tea.Cmd {
	p.cancel()
	p.take++
	p.clip, p.offset, p.recording, p.preparing = voice.Clip{}, 0, true, false
	p.started = time.Now()
	ctx, cancel := context.WithCancel(r.ctx())
	p.cancelRecord = cancel
	audio, token, take := r.deps().Voice, p.token, p.take
	r.clearNotice()
	return func() tea.Msg {
		clip, err := audio.Record(ctx)
		return voiceReadyMsg{token: token, take: take, clip: clip, err: err}
	}
}

func (p *voicePage) play(r rootAPI) tea.Cmd {
	if len(p.clip.Data) == 0 || p.loading {
		return nil
	}
	if p.playing {
		p.stopPlayback(true)
		return nil
	}
	ctx, cancel := context.WithCancel(r.ctx())
	p.cancelPlay, p.playing, p.playStarted = cancel, true, time.Now()
	p.playSeq++
	token, seq, clip, offset, audio := p.token, p.playSeq, p.clip, p.offset, r.deps().Voice
	r.clearNotice()
	return func() tea.Msg { return voicePlayedMsg{token: token, seq: seq, err: audio.Play(ctx, clip.Data, offset)} }
}

func (p *voicePage) stopPlayback(pause bool) {
	if p.cancelPlay != nil {
		p.cancelPlay()
		p.cancelPlay = nil
	}
	if p.playing && pause {
		p.offset += time.Since(p.playStarted)
	}
	if !pause {
		p.offset = 0
	}
	p.playSeq++ // ignore late cancellation/completion from the previous playback
	p.playing = false
}

func (p *voicePage) cancel() {
	if p.cancelLoad != nil {
		p.cancelLoad()
		p.cancelLoad = nil
	}
	if p.cancelRecord != nil {
		p.cancelRecord()
		p.cancelRecord = nil
	}
	p.stopPlayback(false)
}

func (p *voicePage) send(r rootAPI) tea.Cmd {
	if len(p.clip.Data) == 0 {
		return nil
	}
	p.stopPlayback(false)
	p.sending = true
	ctx, client, clip, chat, token := r.ctx(), r.deps().Client, p.clip, p.chat, p.token
	return func() tea.Msg {
		_, err := client.SendVoice(ctx, chat, clip.Data, clip.Seconds, clip.Waveform)
		return voiceSentMsg{token: token, err: err}
	}
}

func (p *voicePage) help() []key.Binding {
	if p.sending {
		return nil
	}
	if p.loading || p.preparing {
		return []key.Binding{keys.Back}
	}
	if p.recording {
		return voiceRecordHelp
	}
	if p.received {
		return voiceListenHelp
	}
	return voiceDraftHelp
}

func (p *voicePage) view(r rootAPI, width, height int) string {
	title := "Voice message to " + chatTitle(r.chat(), r.own())
	if p.received {
		title = "Voice message in " + chatTitle(r.chat(), r.own())
	}
	state := "Ready"
	switch {
	case p.loading:
		state = "Downloading voice message…"
	case p.preparing:
		state = "Preparing recording…"
	case p.sending:
		state = "Sending voice message…"
	case p.recording:
		state = "● Recording " + voiceTime(time.Since(p.started)) + " · Enter stops; nothing is sent yet."
	case p.playing:
		state = "▶ Playing " + voiceTime(p.offset+time.Since(p.playStarted)) + " / " + voiceTime(time.Duration(p.clip.Seconds)*time.Second)
	case p.offset > 0:
		state = "Paused at " + voiceTime(p.offset)
	case len(p.clip.Data) > 0:
		state = "Voice message · " + voiceTime(time.Duration(p.clip.Seconds)*time.Second)
	}
	return pageStyle(width, height).Render(paneTitleStyle.Render(title) + "\n\n" + state + "\n\n" + keyLine(p.help()))
}

func voiceTime(d time.Duration) string {
	seconds := max(0, int(d/time.Second))
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

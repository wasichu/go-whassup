package ui

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

const (
	ada  = "15551234567@s.whatsapp.net"
	bob  = "15559876543@s.whatsapp.net"
	book = "120363000000000000@g.us"
)

var day = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	applied []config.Config // configs passed to ApplyConfig
	cfgPath string
	t       *testing.T
	m       tea.Model
	client  *watest.Client
	st      *store.Store
	pending []chan tea.Msg // blocked commands (e.g. waitEvent), as Bubble Tea keeps them
	raw     []string       // escape sequences sent with tea.Raw
	cache   media.Cache
	opened  []string // files passed to OpenFile
	openErr error
	saves   int // configSavedMsg delivered (see waitSaves)
}

// withGraphics simulates a true-color terminal with Kitty graphics support.
func (h *harness) withGraphics() {
	m := h.model()
	m.deps.Graphics = termcaps.Graphics{Kitty: true}
	h.m = m
	h.send(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
}

func (h *harness) rawContains(s string) bool {
	for _, r := range h.raw {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
}

// newHarness builds the model wired like internal/app: recorder first,
// then the UI subscription. It seeds two chats.
func newHarness(t *testing.T, paired bool) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "messages.db"), nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := st.SaveMessages(t.Context(), []wa.Message{
		{ID: "a1", Chat: ada, Sender: ada, Timestamp: day, Kind: wa.KindText, Text: "hello from ada"},
		{ID: "b1", Chat: bob, Sender: bob, Timestamp: day.Add(-time.Hour), Kind: wa.KindText, Text: "hi from bob"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SaveChats(t.Context(), []wa.Chat{{JID: ada, Name: "Ada"}, {JID: bob, Name: "Bob"}}); err != nil {
		t.Fatalf("seed chats: %v", err)
	}

	client := &watest.Client{Paired: paired, ConnectEvents: []wa.Event{wa.Connected{}}}
	(&store.Recorder{Store: st, Log: log}).Attach(client)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	sub := wa.Subscribe(client, 64, log)
	t.Cleanup(sub.Close)

	h := &harness{t: t, client: client, st: st, cfgPath: filepath.Join(t.TempDir(), "config.toml"),
		cache: media.Cache{Dir: filepath.Join(t.TempDir(), "media")}}
	h.m = New(ctx, Deps{
		Client: client, Store: st, Log: log,
		Config: config.Default(), ConfigPath: h.cfgPath,
		Media: h.cache,
		OpenFile: func(path string) error {
			h.opened = append(h.opened, path)
			return h.openErr
		},
		ApplyConfig: func(_ context.Context, c config.Config) error {
			h.applied = append(h.applied, c)
			return nil
		},
	}, sub)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.run(h.m.Init())
	h.settle()
	return h
}

// send delivers msg and runs the resulting commands.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(msg)
	h.run(cmd)
}

// run executes cmd and feeds results back, recursively. A command that
// blocks (the pending waitEvent) is parked; settle delivers it later.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		h.deliver(msg)
	case <-time.After(20 * time.Millisecond):
		h.pending = append(h.pending, done)
	}
}

// settle delivers results from parked commands that have since completed,
// until nothing more happens.
func (h *harness) settle() {
	h.t.Helper()
	for progress := true; progress; {
		progress = false
		parked := h.pending
		h.pending = nil
		for _, ch := range parked {
			select {
			case msg := <-ch:
				h.deliver(msg)
				progress = true
			case <-time.After(20 * time.Millisecond):
				h.pending = append(h.pending, ch)
			}
		}
	}
}

func (h *harness) deliver(msg tea.Msg) {
	h.t.Helper()
	switch msg := msg.(type) {
	case nil:
	case tea.RawMsg:
		h.raw = append(h.raw, fmt.Sprint(msg.Msg))
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		if _, ok := msg.(configSavedMsg); ok {
			h.saves++
		}
		h.send(msg)
	}
}

// waitFor settles until cond holds: background work (a config save, a
// download, a decode) can take longer than settle's short wait on a slow
// machine such as CI. It fails the test after 5 seconds.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		h.settle()
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s:\n%s", what, h.view())
}

// waitSaves waits until n config saves (configSavedMsg) have been
// delivered. h.applied is safe to read afterwards: ApplyConfig ran before
// each save's message was sent.
func (h *harness) waitSaves(n int) {
	h.t.Helper()
	h.waitFor(fmt.Sprintf("%d config saves", n), func() bool { return h.saves >= n })
}

func (h *harness) key(k string) {
	h.t.Helper()
	switch k {
	case "enter":
		h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	case "esc":
		h.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	case "down":
		h.send(tea.KeyPressMsg{Code: tea.KeyDown})
	case "up":
		h.send(tea.KeyPressMsg{Code: tea.KeyUp})
	case "left":
		h.send(tea.KeyPressMsg{Code: tea.KeyLeft})
	case "right":
		h.send(tea.KeyPressMsg{Code: tea.KeyRight})
	case "ctrl+c", "ctrl+o", "ctrl+t", "ctrl+r":
		h.send(tea.KeyPressMsg{Code: rune(k[len(k)-1]), Mod: tea.ModCtrl})
	default:
		for _, r := range k {
			h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

func (h *harness) view() string { return h.m.View().Content }

func (h *harness) model() Model { return h.m.(Model) }

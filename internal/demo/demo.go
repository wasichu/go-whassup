// Package demo runs the real TUI on made-up chats (go-whassup demo): no
// account, no network, nothing kept. Everything lives in a temporary
// directory removed on exit. It's for trying go-whassup, and for
// screenshots without anyone's real data. Phone numbers are from the
// 555-01xx range, which is reserved for fiction.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/logging"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/termcaps"
	"github.com/wasichu/go-whassup/internal/ui"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

// Run starts the TUI on a freshly seeded, temporary store and blocks until
// it exits.
func Run(ctx context.Context) (err error) {
	dir, err := os.MkdirTemp("", "go-whassup-demo-")
	if err != nil {
		return fmt.Errorf("creating demo directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()

	log, logCloser, err := logging.Open(filepath.Join(dir, "demo.log"), false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, logCloser.Close()) }()
	st, err := store.Open(ctx, filepath.Join(dir, "messages.db"), nil)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()

	now := time.Now()
	if err := seed(ctx, st, now); err != nil {
		return fmt.Errorf("seeding demo chats: %w", err)
	}
	client := &watest.Client{
		Paired: true, Own: me, ConnectEvents: []wa.Event{wa.Connected{}},
		Now: time.Now, DownloadFor: downloadFor,
	}
	// As in the app: the recorder before the UI's subscription.
	(&store.Recorder{Store: st, Log: log}).Attach(client)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go script(ctx, client)

	return ui.Run(ctx, ui.Deps{
		Client: client, Store: st, Log: log,
		Config: config.Default(), ConfigPath: filepath.Join(dir, "config.toml"),
		Graphics:  termcaps.DetectGraphics(os.Getenv, termcaps.Tmux),
		Notify:    termcaps.DetectNotify(os.Getenv, termcaps.Tmux),
		FocusHint: termcaps.FocusHint(os.Getenv, termcaps.Tmux),
		Media:     media.NewSwitchable(filepath.Join(dir, "media")),
		OpenFile:  media.Open,
		Notice:    "Demo: made-up chats. Nothing is sent, and it's all gone when you quit.",
	})
}

// downloadFor is what "downloading" an attachment yields: a drawn photo,
// or a short text document.
func downloadFor(m wa.Media) []byte {
	if strings.HasPrefix(m.Mime, "image/") {
		return scenePNG(640, 480)
	}
	return []byte("Bletchley trip\n\n09:00  Train from Euston\n11:30  Huts tour\n13:00  Lunch\n")
}

// script makes the demo feel live: messages arrive a little after it starts
// (with notifications, if the terminal isn't focused).
func script(ctx context.Context, c *watest.Client) {
	for _, step := range []struct {
		after time.Duration
		msg   wa.Message
	}{
		{6 * time.Second, wa.Message{ID: "live1", Chat: grace, Sender: grace, Kind: wa.KindText, Text: "Are you coming tonight? 🎉"}},
		{20 * time.Second, wa.Message{ID: "live2", Chat: bookClub, Sender: ada, IsGroup: true, Kind: wa.KindText, Text: "@" + user(me) + " can you bring snacks on Thursday?"}},
	} {
		select {
		case <-ctx.Done():
			return
		case <-time.After(step.after):
		}
		step.msg.Timestamp = time.Now()
		c.Emit(step.msg)
	}
}

func user(jid string) string { u, _, _ := strings.Cut(jid, "@"); return u }

// Package logging sets up the app's file logger. Bubble Tea owns the
// terminal, so nothing logs to stdout/stderr.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Open returns a logger appending to path (created 0600). Debug enables
// debug-level output, including whatsmeow's protocol chatter and message
// text. The caller must Close the returned closer.
func Open(path string, debug bool) (*slog.Logger, io.Closer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening log file: %w", err)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})), f, nil
}

// AtLeast returns a logger that drops records below min, whatever log's own
// level. Used to keep a library's debug output out of the log (e.g.
// whatsmeow while messages are encrypted).
func AtLeast(log *slog.Logger, min slog.Level) *slog.Logger {
	return slog.New(minLevel{h: log.Handler(), min: min})
}

type minLevel struct {
	h   slog.Handler
	min slog.Level
}

func (m minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= m.min && m.h.Enabled(ctx, l)
}
func (m minLevel) Handle(ctx context.Context, r slog.Record) error { return m.h.Handle(ctx, r) }
func (m minLevel) WithAttrs(a []slog.Attr) slog.Handler            { return minLevel{m.h.WithAttrs(a), m.min} }
func (m minLevel) WithGroup(g string) slog.Handler                 { return minLevel{m.h.WithGroup(g), m.min} }

// Package logging sets up the app's file logger. Bubble Tea owns the
// terminal, so nothing logs to stdout/stderr.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
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

// Cap is a minimum level for a logger made with Capped, changeable while
// the app runs (it's checked for every record). The zero value lets
// everything through. Used to keep a library's debug output out of the log
// while messages are encrypted (whatsmeow's includes message text).
type Cap struct {
	on  atomic.Bool
	min atomic.Int64
}

// Set drops records below min from now on.
func (c *Cap) Set(min slog.Level) {
	c.min.Store(int64(min))
	c.on.Store(true)
}

// Clear lets every record through again.
func (c *Cap) Clear() { c.on.Store(false) }

func (c *Cap) allows(l slog.Level) bool { return !c.on.Load() || int64(l) >= c.min.Load() }

// Capped returns a logger that drops records below c's level, whatever
// log's own level.
func Capped(log *slog.Logger, c *Cap) *slog.Logger {
	return slog.New(capped{h: log.Handler(), c: c})
}

type capped struct {
	h slog.Handler
	c *Cap
}

func (m capped) Enabled(ctx context.Context, l slog.Level) bool {
	return m.c.allows(l) && m.h.Enabled(ctx, l)
}
func (m capped) Handle(ctx context.Context, r slog.Record) error { return m.h.Handle(ctx, r) }
func (m capped) WithAttrs(a []slog.Attr) slog.Handler            { return capped{m.h.WithAttrs(a), m.c} }
func (m capped) WithGroup(g string) slog.Handler                 { return capped{m.h.WithGroup(g), m.c} }

// Package logging sets up the app's file logger. Bubble Tea owns the
// terminal, so nothing logs to stdout/stderr.
package logging

import (
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

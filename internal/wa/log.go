package wa

import (
	"context"
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogLogger adapts slog to whatsmeow's printf-style logger interface.
type slogLogger struct {
	log *slog.Logger
	// onError, if set, sees every error record unformatted: whatsmeow
	// reports some failures only by logging them (see clientError).
	onError func(format string, args []any)
}

func newLogger(log *slog.Logger, module string) waLog.Logger {
	return slogLogger{log: log.With("module", module)}
}

func (l slogLogger) Errorf(msg string, args ...any) {
	if l.onError != nil {
		l.onError(msg, args)
	}
	l.emit(slog.LevelError, msg, args)
}
func (l slogLogger) Warnf(msg string, args ...any)  { l.emit(slog.LevelWarn, msg, args) }
func (l slogLogger) Infof(msg string, args ...any)  { l.emit(slog.LevelInfo, msg, args) }
func (l slogLogger) Debugf(msg string, args ...any) { l.emit(slog.LevelDebug, msg, args) }

func (l slogLogger) Sub(module string) waLog.Logger {
	return slogLogger{log: l.log.With("sub", module), onError: l.onError}
}

func (l slogLogger) emit(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	// Skip formatting entirely when the level is off; whatsmeow's debug
	// output is very chatty.
	if !l.log.Enabled(ctx, level) {
		return
	}
	l.log.Log(ctx, level, fmt.Sprintf(msg, args...))
}

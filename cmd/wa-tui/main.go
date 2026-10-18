// Command wa-tui is a WhatsApp client for the terminal.
package main

import (
	"fmt"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wa-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}

	// Bubble Tea owns stdout/stderr while running, so all logging goes to a file.
	logFile, err := os.OpenFile(p.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	slog.Info("starting", "data_dir", p.DataDir)

	if _, err := tea.NewProgram(ui.New()).Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

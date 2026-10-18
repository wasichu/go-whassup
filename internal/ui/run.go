package ui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Run starts the TUI and blocks until it exits or ctx is cancelled.
func Run(ctx context.Context) error {
	_, err := tea.NewProgram(New(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // cancelled from outside (e.g. SIGTERM): a clean exit
	}
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

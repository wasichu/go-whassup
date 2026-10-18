package ui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

// eventBuffer sizes the UI's event subscription; history sync can burst.
const eventBuffer = 256

// Run starts the TUI and blocks until it exits or ctx is cancelled. Any
// store recorder must already be attached to deps.Client.
func Run(ctx context.Context, deps Deps) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // unblocks a pending waitEvent on exit
	sub := wa.Subscribe(deps.Client, eventBuffer, deps.Log)
	defer sub.Close()

	_, err := tea.NewProgram(New(ctx, deps, sub), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // cancelled from outside (e.g. SIGTERM): a clean exit
	}
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

// Command wa-tui is a WhatsApp client for the terminal. See docs/usage.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/wasichu/go-whassup/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := app.Run(ctx, os.Args[1:], app.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wa-tui:", err)
		os.Exit(1)
	}
}

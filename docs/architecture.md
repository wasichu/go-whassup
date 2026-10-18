# Architecture

```
cmd/wa-tui (main: signals + exit code only)
   └─► internal/app            composition root: args, paths, logging, wiring
         ├─► internal/paths     XDG locations (data dir, session.db, log)
         ├─► internal/logging   slog → log file
         ├─► internal/ui        Bubble Tea v2 TUI          ─┐
         ├─► internal/headless  login / listen commands     ├─► wa.Client (interface)
         └─► internal/wa        whatsmeow adapter         ◄─┘     └─► session.db
                internal/wa/watest   fake wa.Client for tests
                internal/store       (step 2) our chat/message DB
```

## Rules of the layout

- **`cmd/wa-tui/main.go` stays tiny.** It sets up the signal context, calls `app.Run`,
  and maps the error to an exit code.
- **`internal/app` wires things together and holds no logic.** If a function there
  grows decisions, move them to the package that owns the concern.
- **Extend before adding.** Put new code in the package that owns the concern. Add a
  package only when there's a genuinely new concern, and add it to this diagram.
- **`internal/wa`** is the only package that imports `go.mau.fi/whatsmeow`. This is
  enforced by depguard in `.golangci.yml`. Everything else depends on the
  `wa.Client` interface and plain `wa.Event` values (strings and `time.Time`,
  never whatsmeow types).
- **`internal/wa/watest`** holds the shared fake `wa.Client`. Use it in tests for
  `headless`, `ui` and future packages rather than writing ad-hoc fakes.

## Event flow and goroutines

whatsmeow calls its event handler on its own goroutines. `WhatsApp.handleEvent`
translates each event into a `wa.Event` and passes it to the handler set with
`SetEventHandler`.

**Consumers use `wa.Subscribe`.** It registers a handler that pushes events onto a
buffered channel, `Subscription.C`, which a single goroutine then consumes:

- **headless:** `Console.Follow` loops over `sub.C` and prints.
- **TUI:** a `tea.Cmd` receives the next event from `sub.C` and returns it as a
  `tea.Msg`. `Update()` handles it and re-issues the command. This is the standard
  Bubble Tea pattern for waiting on activity, and state changes only in `Update()`.

`Subscribe` never blocks the protocol goroutines. If the buffer is full, events are
**dropped and counted** (`Subscription.Dropped()`), and consumers surface the
count. That's fine for display. **Anything that must not lose data (the message
store in step 2) must not consume a lossy subscription.** It needs its own handler
that writes synchronously or blocks deliberately.

Events we don't handle yet (history sync, app-state sync, receipts, presence) are
logged at debug level. A fresh pairing produces a flood of them.

## Terminal ownership

While the TUI runs, Bubble Tea owns stdout/stderr. All logging goes through `slog`
(`internal/logging`) to `$XDG_STATE_HOME/go-whassup/wa.log`, including whatsmeow's
logger via the adapter in `internal/wa/log.go`. forbidigo bans `fmt.Print*` and
`log.Print*`. Headless commands write through `Console.Out`, an `io.Writer`, so
tests can capture the output.

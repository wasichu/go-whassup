# Architecture

```
cmd/wa-tui ──► internal/ui ──► internal/wa (interface) ◄── internal/wa (whatsmeow impl)
     │                                                          │
     └──────────────► internal/paths                            └──► session.db (whatsmeow store)
                                                 internal/store ──► (step 2: our chat/message DB)
```

## Layers

- **`internal/wa`** is the only package that imports `go.mau.fi/whatsmeow`
  (enforced by depguard in `.golangci.yml`). It exposes:
  - `wa.Client`, the interface everything else depends on;
  - plain event values (`Connected`, `Disconnected`, `LoggedOut`, `PairSuccess`,
    `PairFailed`, `Message`) that carry strings and `time.Time`, never whatsmeow types.
- **`internal/ui`** holds the Bubble Tea v2 models. They talk to `wa.Client` and receive events as
  `tea.Msg`. Tests drive `Update()` with a fake client.
- **`cmd/wa-tui`** handles wiring and the headless `login`/`listen` subcommands.

## Event flow and goroutines

whatsmeow calls its event handler on its own goroutines. `WhatsApp.handleEvent`
translates each event into a `wa.Event` and passes it to the handler registered
with `SetEventHandler`. That handler must never touch UI state:

- In the TUI, it forwards events with `tea.Program.Send(evt)`, so state changes only
  in `Update()`.
- In headless mode, it pushes events onto a buffered channel. If the channel is full,
  the event is logged and dropped; the handler never blocks whatsmeow.

Events we don't handle yet (history sync, app-state sync, receipts, presence) are
logged at debug level. A fresh pairing produces a flood of them.

## Terminal ownership

While the TUI runs, Bubble Tea owns stdout/stderr. All logging goes through `slog` to
`$XDG_STATE_HOME/go-whassup/wa.log`, including whatsmeow's logger via the adapter
in `internal/wa/log.go`. forbidigo bans `fmt.Print*`/`log.Print*`. Headless
subcommands write user output with explicit `fmt.Fprint*(os.Stdout, …)`.

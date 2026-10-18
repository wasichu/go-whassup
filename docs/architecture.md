# Architecture

```
cmd/wa-tui (main: signals + exit code only)
   └─► internal/app            composition root: args, paths, logging, wiring
         ├─► internal/paths     XDG locations (config, data dir, DBs, log)
         ├─► internal/config    typed Config (TOML)
         ├─► internal/logging   slog → log file
         ├─► internal/ui        Bubble Tea v2 TUI          ─┐
         ├─► internal/headless  login / listen commands     ├─► wa.Client (interface)
         ├─► internal/store     messages.db + Recorder      │
         └─► internal/wa        whatsmeow adapter         ◄─┘     └─► session.db
                internal/wa/watest   fake wa.Client for tests
                internal/sqlite      shared SQLite open (driver, DSN, 0600) for both DBs
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
translates each event (`internal/wa/translate.go`) into a `wa.Event` and emits it
to every handler registered with `AddEventHandler`.
- Handlers run in registration order on that goroutine.
- `AddEventHandler` returns a `remove` func.
- `wa.Handlers` is the shared implementation, embedded by both the real client and
  `watest.Client`.

There are two kinds of consumers:

- **Must not lose events** (`store.Recorder`): register a handler directly
  and do the work synchronously. This deliberately slows whatsmeow down instead of
  dropping data.
- **Display** (headless output, TUI): use `wa.Subscribe`. It pushes events onto a
  buffered channel, `Subscription.C`, which a single goroutine then consumes.
  `Subscription.Close` removes the handler.
  - **headless:** `Console.Follow` loops over `sub.C` and prints.
  - **TUI:** a `tea.Cmd` receives the next event from `sub.C` and returns it as a
    `tea.Msg`. `Update()` handles it and re-issues the command. State changes only in
    `Update()`.
    - On `Message`/`HistorySync`, the UI **reloads from the store** in a command. This
      relies on `internal/app` registering the recorder *before* `ui.Run` subscribes,
      so the row exists by the time the UI hears about it.
    - Store reads and sends run in commands that close over values (ctx, deps, chat
      key), never the model.

  `Subscribe` never blocks. If its buffer is full, events are **dropped and counted**
  (`Subscription.Dropped()`), and consumers surface the count.

## Translation rules (`internal/wa/translate.go`)

- **Stable keys.** Chat and sender keys are JIDs without the device suffix.
  - WhatsApp addresses some users by a privacy ID (`…@lid`). Keys prefer the
    phone-number form so one person maps to one chat. The order of preference is:
    1. the message's own alternate address (`SenderAlt`/`RecipientAlt`, or the
       conversation's `PnJID` in history);
    2. whatsmeow's LID→phone-number store.
  - An unresolvable LID is kept as the key. Messages are never dropped for this.
- **Kinds.** `Message.Kind` is `text`, `image`, `video`, `document`, `reaction`, or a
  name derived from the protobuf field (e.g. `sticker`). `Message.Text` is the body or
  caption only. `Summary()` renders `[kind] text` for display.
- **Skipped:** protocol messages (revokes, edits, key distribution), status updates
  (`status@broadcast`, i.e. stories), and the `INITIAL_STATUS_V3` history blob.
  **Edits and deletes are not applied yet.**
- **Sent messages.** whatsmeow doesn't echo messages this device sends. After the
  server acknowledges one, `SendText` emits it as a normal `wa.Message` with
  `FromMe` set, so the recorder and UI see it through the same path as everything
  else. `SendText` returns only an error; there is no second path.
- **Push name.** On each `Connected`, we send presence **`unavailable`** once. This
  registers our display name, so recipients don't see "-" as the sender. "Available"
  would also mark the user online and switch on active read receipts, which we avoid.
- **History sync** arrives as `wa.HistorySync{Chats, Messages, PushNames}`, parsed with
  whatsmeow's `ParseWebMessage`. Empty batches aren't emitted. App-state sync,
  receipts and presence are still only logged at debug level.

## TUI structure (`internal/ui`)

- **One root `Model`** with a `screen` (main, login), a `?` help overlay, and sub-state
  per screen (`chatList`, `conversation`, `login`). Everything changes only in `Update`.
- **`keys.go` is the single keymap.** Handlers use `key.Matches`, and hints/help render
  the same bindings with `bubbles/help`.
- **Focus gates shortcuts.** Keys reach the message input only when it's focused, and
  global shortcuts only fire from the list.
- **Colours** use the 16-colour ANSI palette, so they follow the user's terminal
  theme. The only fixed colours are the QR's black/white, because scanners need them
  and themes remap ANSI black/white.
- **Pairing** commands use the app-lifetime context (it bounds the resulting
  connection). Esc cancels with `wa.Client.CancelPairing`, never by cancelling that
  context.

## Terminal ownership

While the TUI runs, Bubble Tea owns stdout/stderr. All logging goes through `slog`
(`internal/logging`) to `$XDG_STATE_HOME/go-whassup/wa.log`, including whatsmeow's
logger via the adapter in `internal/wa/log.go`. forbidigo bans `fmt.Print*` and
`log.Print*`. Headless commands write through `Console.Out`, an `io.Writer`, so
tests can capture the output.

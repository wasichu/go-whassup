# Usage

```
wa-tui [-debug]                 start the TUI (placeholder for now)
wa-tui login [-debug] [phone]   link this device with a phone-number code
wa-tui listen [-debug]          connect headless and print incoming messages
```

- Flags may come before or after the subcommand (`wa-tui -debug login` ==
  `wa-tui login -debug`).
- `phone` is in international format. Quote it if it contains spaces:
  `wa-tui login "+1 555 123 4567"`. If omitted, `login` prompts for it.
- `-debug` enables whatsmeow's very verbose protocol logging and logs message text.
- `wa-tui -h` prints usage.
- Via just: `just run login +15551234567`, `just run listen`, and `just logs` to tail
  the log.

# Usage

```
wa-tui [-debug]                     start the TUI (placeholder for now)
wa-tui login [-debug] [phone]       link this device with a phone-number code
wa-tui listen [-debug]              connect and print incoming messages
wa-tui db chats                     list stored chats (reads messages.db; no network)
wa-tui db messages [-n N] <chat>    show a stored chat's newest N messages (default 20, 0 = all)
```

- `login` and `listen` also **store** everything they receive, including the history
  sync the phone pushes after linking, in `messages.db`.
- History sync only happens when a device is linked; see `storage.md` to backfill.
- The `db` commands only read the local database. They work offline and while
  `listen` is running.
- `<chat>` can be a JID, a phone number (`+1 555 123 4567`), or part of the chat's name
  (`"book club"`). Ambiguous names list the candidates.

- Flags may come before or after the subcommand (`wa-tui -debug login` ==
  `wa-tui login -debug`).
- `phone` is in international format. Quote it if it contains spaces:
  `wa-tui login "+1 555 123 4567"`. If omitted, `login` prompts for it.
- `-debug` enables whatsmeow's very verbose protocol logging and logs message text.
- `wa-tui -h` prints usage.
- Via just: `just run login +15551234567`, `just run listen`, and `just logs` to tail
  the log.

# Usage

```
wa-tui [-debug]                     start the TUI
wa-tui login [-relink] [phone]      link this device with a phone-number code;
                                    -relink first unlinks the current session
wa-tui listen [-debug]              connect and print incoming messages
wa-tui db chats                     list stored chats (reads messages.db; no network)
wa-tui db messages [-n N] <chat>    show a stored chat's newest N messages (default 20, 0 = all)
```

- `login` and `listen` also **store** everything they receive, including the history
  sync the phone pushes after linking, in `messages.db`.
- History sync only happens when a device is linked. To backfill, run `wa-tui login -relink`
  (stored messages are kept and deduplicated).
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

## TUI

`wa-tui` opens on the **chat list** (left) and the selected chat (right). It works
offline from `messages.db` and connects in the background; the connection state is in
the status line. If the device isn't linked, it opens the **login screen** instead.

### Main screen

| Key | Chat list focused | Message box focused |
| --- | --- | --- |
| `↑`/`↓`, `k`/`j` | select chat | (typed) |
| `enter`, `tab`, `→`, `l` | write in this chat | `enter` sends |
| `esc` | | back to chat list |
| `pgup`/`pgdn` | scroll messages | scroll messages |
| `L` | login screen: link, or re-link | (typed) |
| `,` | settings | (typed) |
| `?` | key help (any key closes) | (typed) |
| `q` | quit | (typed) |
| `ctrl+c` | quit | quit |

Shortcuts only act from the chat list. In the message box `q`, `L`, `,` and `?` are just
characters. Hints in the status line are generated from the same keymap as the
handlers (`internal/ui/keys.go`), so they can't drift.

### Login screen

- **Not linked:** choose *phone number code* (type your number and enter the
  8-character code on the phone) or *QR code* (scan from Linked devices › Link a device).
  - The QR is drawn black-on-white whatever your theme. It's about 61×31 cells, so the
    window needs about 33 rows. Zooming out makes it physically smaller. Instructions go beside the QR when the window is wide,
    and the white border shrinks from 2 modules to 1 if that makes it fit.
  - If it still doesn't fit, the screen says exactly how many rows or columns are
    missing. Make the window bigger or zoom out (Cmd − / Ctrl −): the QR appears as soon
    as it fits, and pairing keeps running meanwhile. Or use the phone code.
- **Linked:** *Re-link*, after a y/n confirmation. It does the same as
  `wa-tui login -relink`.
- **Esc:** while waiting for the code or a scan, cancels pairing; otherwise goes back.
- **After you're logged out remotely** (e.g. from the phone), the login screen opens with
  the reason.

### Settings screen (`,`)

- Lists every setting in `config.toml`, grouped by section, with its description (the
  same comment written into the file).
- `↑`/`↓` to choose; `enter` or space toggles yes/no settings or edits numbers (`enter`
  saves, `esc` cancels).
- **Each committed change is validated, saved to the file and applied immediately.**
  For example, lowering `keep_days` prunes right away. Settings that only matter when
  linking are labelled as such.
- Invalid values are explained inline, and the previous value is kept.

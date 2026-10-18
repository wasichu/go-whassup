# Usage

```
wa-tui [-debug]                     start the TUI
wa-tui login [-relink] [phone]      link this device with a phone-number code;
                                    -relink first unlinks the current session
wa-tui listen [-debug]              connect and print incoming messages
wa-tui db chats                     list stored chats (reads messages.db; no network)
wa-tui db messages [-n N] <chat>    show a stored chat's newest N messages (default 20, 0 = all)
wa-tui db search [-n N] <words>     search stored messages, newest matches first
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
| `/` | find a chat or contact (new chats too) | (typed) |
| `s` | search message text | (typed) |
| `o` | this chat's attachments: open (downloads if needed) | (typed) |
| `a` | send a file to this chat | (typed) |
| `i` | chat info: number, or a group's topic and members | (typed) |
| `L` | login screen: link, or re-link | (typed) |
| `,` | settings | (typed) |
| `?` | key help (any key closes) | (typed) |
| `q` | quit | (typed) |
| `ctrl+c` | quit | quit |

Shortcuts only act from the chat list. In the message box `q`, `/`, `s`, `o`, `a`, `i`, `L`, `,` and `?` are just
characters. Hints in the status line are generated from the same keymap as the
handlers (`internal/ui/keys.go`), so they can't drift.

### Search (`s`)

- Searches the text of messages stored on this computer (WhatsApp's servers can't
  search; see `storage.md`). All words must match, the last may be a prefix, and
  accents are ignored (`cafe` finds *Café*). Newest matches first, up to 200.
- `enter` opens the chat at that message, highlighted. A hit older than the 500 most
  recent messages loaded per chat says so instead.
- **Unavailable while encryption is on.** Finding chats and contacts by name (`/`) still
  works.

### Groups and mentions

- Group names, topics and member lists come from WhatsApp: one request for all your
  groups after connecting (at most hourly), and a refresh of a single group when it
  changes. Group headers show the member count.
- **`i`** shows a group's topic and members (admins first), or a person's number and
  the name they chose for themselves.
- **Mentions** like `@15551234567` show as **@Ada** when the number is a known contact,
  and as a highlighted **@you** when it's you.

### Attachments

- Messages with an attachment show its size, and a ✓ once it's downloaded.
- **`o`** lists the open chat's attachments, newest first. `enter` downloads one if
  needed (into `~/.local/share/go-whassup/media/`) and opens it with your system's
  default app.
  - **Only ordinary document and media types are opened** (images, video, audio, PDF,
    office files, text, zip, …). The sender chooses the file name, so anything else,
    such as `.command`, `.desktop`, `.html` or scripts, is saved as `.bin` and **not
    opened**; the status line says where it was saved.
  - Media older than WhatsApp keeps on its servers can't be downloaded; you'll be told
    to ask the sender to resend it.
- **`a`** sends a file: type a path (`~` works), `tab` to add an optional caption,
  `enter` to send.
  - JPEG, PNG and WebP go as photos; everything else, including video, audio and HEIC,
    goes as a document. The limit is 100 MB.
  - Your own sent files are cached, so they open without a download.
- Downloaded files are **not encrypted on disk** (see `storage.md`).

### Edits, deletes and reactions

- Edited messages show their latest text with a faint **(edited)**.
- Messages deleted for everyone show *This message was deleted*; their text is wiped
  from `messages.db`, not just hidden. Messages you delete "for me" on your phone
  disappear.
- Reactions appear under the message as `🎉 2  ❤️`, with your own highlighted.
- Sending reactions, edits and deletes from go-whassup isn't supported yet.

### Unread messages

- Chats with unread messages are **bold with a count badge**. Opening one shows a
  "── N new ──" divider and starts there if the new messages don't fit on screen.
- A chat counts as read once it has been **on screen about 1.5 s with the terminal
  focused**, and only after you've pressed a key. Launching the app shows the top chat
  but doesn't read it. Arrowing past chats doesn't read them, and messages arriving while you're
  in another window stay unread. Replying also marks a chat read. Reading a chat on your
  phone clears it here too.
- **What gets sent** is `[privacy] mark_read` (Settings `,`):
  - `local-only` (default): nothing;
  - `read-receipts-off`: your phone and other devices are told, with no blue ticks;
  - `read-receipts-on`: read receipts, like the official apps. If read receipts are off
    in your phone's WhatsApp privacy settings, whatsmeow sends `read-self` instead, so
    your phone's setting is respected. (Groups whose members are addressed by privacy ID
    are untested with `read-receipts-on`.)
- **In tmux**, focus tracking needs `tmux set -g focus-events on` (add it to
  `~/.tmux.conf`). Without it, the status line shows a tip at launch, and chats count as
  read even while you're in another window.
- When you link, the phone's unread counts are adopted. Chats that existed before this
  feature start out read.

### Finder (`/`)

- Fuzzy-search every chat and every contact from your phone's address book by name, or
  by number (`555 123`, `+1-555…`). Type to filter; `↑`/`↓` choose; `enter` opens;
  `esc` goes back.
- A contact you've never messaged is marked **new chat**. Opening it starts a draft:
  type and send, and it becomes a regular chat in the list.
- Chat titles follow the phone: your address-book name for a person, then the name they
  chose, then their number (`+1555…`); a group's subject. The chat with yourself reads
  `… (You)`, as on WhatsApp Web, and typing `you` in the finder finds it. In groups, each sender shows the same way.

### Login screen

- **Not linked:** choose *phone number code* (type your number and enter the
  8-character code on the phone) or *QR code* (scan from Linked devices › Link a device).
  - **In Ghostty or kitty**, the QR is a small inline image, 32×16 cells, using the
    Kitty graphics protocol. Inside **tmux** this needs `set -g allow-passthrough on`
    (and tmux passing 24-bit colour); if either is missing, the QR screen shows a tip. Set `[ui] inline_images = false` (or toggle
    it in Settings) to use text blocks instead.
  - **Elsewhere**, the QR is drawn with text blocks, black-on-white whatever your theme. It's about 61×31 cells, so the
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

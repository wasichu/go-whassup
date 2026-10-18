# Usage

```
go-whassup [-debug]                     start the TUI
go-whassup login [-relink] [phone]      link this device with a phone-number code;
                                        -relink first unlinks the current session
go-whassup listen [-debug]              connect and print incoming messages
go-whassup db chats                     list stored chats (reads messages.db; no network)
go-whassup db messages [-n N] <chat>    show a stored chat's newest N messages (default 20, 0 = all)
go-whassup db search [-n N] <words>     search stored messages, newest matches first
go-whassup db reset                     delete messages.db and the media cache (type "yes");
                                        the way out if stored messages can't be unlocked
```

- `login` and `listen` also **store** everything they receive, including the history
  sync the phone pushes after linking, in `messages.db`.
- History sync only happens when a device is linked. To backfill, run `go-whassup login -relink`
  (stored messages are kept and deduplicated).
- The `db` commands only read the local database. They work offline and while
  `listen` is running.
- `<chat>` can be a JID, a phone number (`+1 555 123 4567`), or part of the chat's name
  (`"book club"`). Ambiguous names list the candidates.

- Flags may come before or after the subcommand (`go-whassup -debug login` ==
  `go-whassup login -debug`).
- `phone` is in international format. Quote it if it contains spaces:
  `go-whassup login "+1 555 123 4567"`. If omitted, `login` prompts for it.
- `-debug` enables whatsmeow's very verbose protocol logging and logs message text.
- `go-whassup -h` prints usage.
- Via just: `just run login +15551234567`, `just run listen`, and `just logs` to tail
  the log.

## TUI

`go-whassup` opens on the **chat list** (left) and the selected chat (right). It works
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
| `ctrl+o` | | this chat's attachments: open (downloads if needed) |
| `ctrl+t` | | send a file to this chat |
| `i` | chat info: number, or a group's topic and members | (typed) |
| `L` | login screen: link, or re-link | (typed) |
| `,` | settings | (typed) |
| `?` | key help (any key closes) | (typed) |
| `q` | quit | (typed) |
| `ctrl+c` | quit | quit |

Letter shortcuts only act from the chat list; in the message box `q`, `/`, `s`, `i`, `L`, `,`
and `?` are just characters. Attachments are handled while writing, like the attach button
in WhatsApp's compose bar, so they use `ctrl+o` and `ctrl+t` (Ctrl+A is the line start, and
a common tmux prefix). Hints in the status line are generated from the same keymap as the
handlers (`internal/ui/keys.go`), so they can't drift.

### Search (`s`)

- Searches the text of messages stored on this computer (WhatsApp's servers can't
  search; see `storage.md`). All words must match, the last may be a prefix, and
  accents are ignored (`cafe` finds *Café*). Newest matches first, up to 200.
- `enter` opens the chat at that message, highlighted. A hit older than the 500 most
  recent messages loaded per chat says so instead.
- **Unavailable while encryption is on**, which the page says as soon as it opens.
  Finding chats and contacts by name (`/`) still works.

### Encryption at rest

Turn on `[encryption] enabled` in Settings (`,`). It asks first, because it **encrypts
message text, reactions and attachment keys only: who you talk to, when, and contact
names stay readable on disk.**
- **It starts right away:** stored messages are encrypted in the background while you
  keep using go-whassup, and the status line says when it's done. With `key_source =
  prompt` you choose the passphrase there first (typed twice, hidden).
- **Turning it off** asks separately (`y` decrypt now, `n` keep them encrypted). No
  passphrase is needed (the key is already unlocked); messages are decrypted back in the
  background, then the key is forgotten and search returns. A keychain entry, if you
  used one, is left in place.
- Changing your mind mid-way is fine: turning it back on while decrypting reuses the
  same key, and quitting mid-way resumes at the next start.
- **Key sources** (`key_source`):
  - `keyring`: your OS keychain; no prompt.
  - `password-command`: e.g. `pass show whatsapp`.
  - `prompt`: asks for a passphrase **in the terminal before the TUI starts**, and needs
    a terminal (so `listen` under a pipe fails clearly).
- **While encrypted:**
  - message search is off;
  - downloads live in a temporary directory deleted on exit;
  - message text is never logged, and `-debug` doesn't include whatsmeow's protocol
    chatter.
- **If it can't unlock** (wrong passphrase, missing keychain entry), `go-whassup` stops and
  says so. Fix the key source, or run `go-whassup db reset` to start over with an empty
  message store (history older than your phone's next sync is lost).

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
- **`ctrl+o`** (in the message box) lists the open chat's attachments, newest first. `enter` downloads one if
  needed (into `~/.local/share/go-whassup/media/`) and opens it with your system's
  default app.
  - **Only ordinary document and media types are opened** (images, video, audio, PDF,
    office files, text, zip, …). The sender chooses the file name, so anything else,
    such as `.command`, `.desktop`, `.html` or scripts, is saved as `.bin` and **not
    opened**; the status line says where it was saved.
  - Media older than WhatsApp keeps on its servers can't be downloaded; you'll be told
    to ask the sender to resend it.
  - Where the terminal can show images, a **preview** appears beside the list: the
    downloaded photo, else WhatsApp's small embedded thumbnail (photos, videos and
    documents received from now on), so nothing is downloaded just to preview.
  - **`d` saves a copy** to `[media] save_dir` (default `~/Downloads/go-whassup`) under
    the sender's file name, or a name like `WhatsApp Image 2026-10-18 at 17.38.12.jpg`.
    It never overwrites, and the extension is always one from the allowlist below.
    Copies aren't encrypted.
- **`ctrl+t`** (in the message box) sends a file. Where the terminal can show images (Kitty graphics: Ghostty,
  kitty; with true color and `[ui] inline_images` on) it opens a **file browser**:
  `↑`/`↓` choose, `enter` opens a folder or picks a file, `←` goes up, `.` shows hidden
  files, `/` switches to typing a path. Resting on a JPEG, PNG or GIF shows a preview
  (WebP and HEIC don't preview; phone photos may appear unrotated). It reopens in the
  last folder you picked from. Picking a file fills in the path and moves to the
  caption; `enter` sends.
- Elsewhere (or after `/`), type a path (`~` works) and `enter` to send.
  - **`tab` completes the path** like a shell: it fills in what all matches share
    (ignoring case, using the real spelling), then the highlighted match; matches are
    listed below and `↑`/`↓` choose. Dotfiles appear once you type a `.`.
  - `shift+tab` moves to the optional caption (so does `tab` once the path is complete).
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
  `~/.tmux.conf`). Without it, the status line shows a tip at launch, chats count as
  read even while you're in another window, and notifications never fire.
- When you link, the phone's unread counts are adopted. Chats that existed before this
  feature start out read.
- **Muted chats** (muted on your phone) show a grey badge instead of a green one.

### Desktop notifications

- A new message raises a desktop notification **while the terminal isn't focused**. When
  it's focused, the chat list shows it instead. Clicking the notification brings your
  terminal forward.
- **Never for muted chats**, your own messages, or backlog that arrives on reconnect
  (messages more than 2 minutes old). A burst from one chat raises one notification a
  minute at most; coming back to the terminal resets that.
- Notifications go through the terminal, which shows them natively, so there's nothing
  to install and message text never appears on a command line. go-whassup picks the
  terminal's protocol: OSC 777 (Ghostty, foot, urxvt), OSC 99 (kitty), otherwise OSC 9
  (iTerm2, WezTerm; terminals without support ignore it). Some
  terminals need notifications enabled in their own settings.
- **In tmux**, notifications need `tmux set -g allow-passthrough on` (to reach the
  terminal) and `focus-events on` (to know you're away); the status line says so at
  launch if they're off.
- `[notifications]` in Settings: `enabled`, and `preview` (`full` shows chat, sender and
  text; `sender` hides the text; `none` hides both). Your OS may keep notifications in
  its history, so with encryption on you may want `sender` or `none`.

### Finder (`/`)

- Fuzzy-search every chat and every contact from your phone's address book by name, or
  by number (`555 123`, `+1-555…`). Type to filter; `↑`/`↓` choose; `enter` opens;
  `esc` goes back.
- A contact you've never messaged is marked **new chat**. Opening it starts a draft,
  shown at the top of the chat list marked `new`: type and send, and it becomes a
  regular chat. Moving down the list without sending drops the draft.
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
  `go-whassup login -relink`.
- **Esc:** while waiting for the code or a scan, cancels pairing; otherwise goes back.
- **After you're logged out remotely** (e.g. from the phone), the login screen opens with
  the reason.

### Settings screen (`,`)

- Lists every setting in `config.toml`, grouped by section, with its description (the
  same comment written into the file).
- `↑`/`↓` to choose; `enter` or space toggles yes/no settings, cycles choices, or edits
  numbers and text (`enter` saves, `esc` cancels). `←`/`→` (or `h`/`l`) cycle a choice
  backwards or forwards.
- Some settings only appear when they apply: `password_command` is listed once
  `key_source` is `password-command`, and choosing that source asks for the command
  straight away (`esc` keeps the previous source).
- **Each committed change is validated, saved to the file and applied immediately.**
  For example, lowering `keep_days` prunes right away. Settings that only matter when
  linking are labelled as such.
- Invalid values are explained inline, and the previous value is kept.

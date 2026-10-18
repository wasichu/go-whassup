# go-whassup

WhatsApp in your terminal: a single, static binary (no cgo, nothing else to install
or run) that links to your phone like WhatsApp Web does, built on
[whatsmeow](https://github.com/tulir/whatsmeow) and
[Bubble Tea](https://github.com/charmbracelet/bubbletea).

> [!WARNING]
> **Unofficial.** go-whassup isn't made or endorsed by WhatsApp or Meta, and using
> third-party clients is against WhatsApp's Terms of Service. Accounts can be banned for
> it. go-whassup avoids bot-like behaviour (no automatic read receipts, no bulk
> fetching, no presence spam), but use it at your own risk, and not with an account you
> can't afford to lose.

## Features

- Chats, groups and messages, with history synced from your phone and kept locally, so
  it works offline and starts instantly.
- Unread counts and a "new" divider. Reading a chat sends nothing unless you choose
  to (`[privacy] mark_read`).
- Shows edits, deletes and reactions (sending them isn't supported yet), and @-mentions
  by name.
- Attachments: open them, save a copy, or send files (a file browser with image
  previews in terminals that can show images, tab-completed paths elsewhere).
- Find any chat or contact (`/`), and full-text search of stored messages (`s`).
- Desktop notifications through your terminal, skipping muted chats.
- Optional encryption of stored messages (keychain, a password command, or a
  passphrase), turned on or off from Settings without restarting.
- Linking by phone-number code or QR code (shown as an image in Ghostty and kitty,
  including inside tmux).

## Install

With Go 1.26 or newer:

```sh
go install github.com/wasichu/go-whassup/cmd/go-whassup@latest
```

Or download a binary for macOS, Linux or Windows from the releases page and put it on
your `PATH`. Windows builds are untested, and the keychain key source isn't available
there.

## Getting started

```sh
go-whassup
```

The first time, it asks how to link: with a code you type into your phone (WhatsApp ›
Settings › Linked devices › Link a device › "Link with phone number instead"), or by
scanning a QR code. Your history arrives over the next minutes as the phone sends it.

Press `?` for every key. The essentials: `↑`/`↓` choose a chat, `enter` to write,
`esc` back to the list, `/` find, `s` search, `,` settings, `q` quit.

There are also headless commands (`go-whassup login`, `listen`, and `db …` to read
stored messages); see `go-whassup -h`.

## Where things live

| What | Where |
| --- | --- |
| Settings | `~/.config/go-whassup/config.toml` (also editable in the app with `,`) |
| Messages and session | `~/.local/share/go-whassup/` |
| Log | `~/.local/state/go-whassup/wa.log` |

`XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` are honoured, on macOS too.
The session database is the key to your account: keep it private.

## Documentation

[docs/usage.md](docs/usage.md) covers every command and key,
[docs/config.md](docs/config.md) every setting, and [docs/](docs/) the design for
contributors (start with [AGENTS.md](AGENTS.md)).

## License

[MPL-2.0](LICENSE), like whatsmeow.

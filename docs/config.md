# Configuration

`$XDG_CONFIG_HOME/go-whassup/config.toml` (default `~/.config/go-whassup/config.toml`).
The file is optional: if it's missing, the defaults apply. The Settings screen in the
TUI's Settings screen (`,`) reads and writes it.

## Design (`internal/config`)

- **One typed struct**, `config.Config`, with defaults in `config.Default()`. The app
  loads it once in `internal/app` and passes values down. No other package reads the
  file or looks settings up by string key.
- **TOML via `github.com/pelletier/go-toml/v2`**, a single module with no
  dependencies of its own. We chose it over viper, which adds 12 modules / 52
  packages, lowercases keys, and on write dumps merged state and drops comments.
  `go-toml/v2` also writes per-field comments from `comment:"…"` struct tags.
- **Strict loading.** Unknown keys are errors naming the bad key, so typos fail
  loudly. Settings missing from the file keep their defaults. `Validate()` reports
  every invalid value.
- **Saving** (`config.Save`) validates first, then writes atomically (temp file in
  the same directory, then rename) with mode `0600`. The file gets a header and a
  comment on each setting, generated from the struct tags. **Hand-written comments
  are lost on save**; that's the accepted trade-off for TUI editing.
- **Precedence:** defaults < file < command-line flags (`-debug`). Environment
  variables are not supported (add them only when there's a real need).
- **Settings screen.** `Config.Fields()` discovers settings by reflection over the
  struct tags: bool and int fields; string fields with a `choices:"a,b"` tag (enter or
  →/← cycles through them) or `text:"true"` (free text). A `confirm:"…"` tag makes Settings ask y/n
  before the value changes, and `confirm_off:"…"` asks a different question when a
  bool is turned off. Both take `question|explanation|yes label|no label` (later parts
  optional; a lone text gets "Change <name>?" and Yes/No). Tag `applies:"pairing"` marks link-time-only settings.
  `shown_when:"key=value"` lists a setting only while a sibling has that value
  (`password_command` only with `key_source = password-command`). A choice that reveals an
  empty text setting asks for it at once, and Esc drops the choice too. The TUI renders and edits them generically, so a new setting appears there
  with no UI code. A change goes through `Validate` → `Save` → `ApplyConfig` (wired in
  `internal/app`), which updates the client's pairing options and the recorder's
  retention, then prunes.
- **Adding a setting:**
  1. Add a field with `toml` and `comment` tags (plus `applies:"pairing"` if it only
     matters when linking).
  2. Set its default in `Default()`. If the running app must react, extend `ApplyConfig`
     in `internal/app`.
  3. Add any validation to `Validate()`.
  4. Document it in the table below.
  5. Extend the round-trip test.

## Settings

### `[history]`

| Key | Default | Meaning |
| --- | --- | --- |
| `request_full_sync` | `false` | Ask the phone for about 1 year of history when linking, instead of its default of about 3 months. **Only affects pairing** (`go-whassup login`); to change it later, re-link. |
| `full_sync_days` | `0` | With `request_full_sync`: how many days to ask for (0 = the phone's full-sync default). Invalid without `request_full_sync`, because WhatsApp only reads it alongside the full-sync flag. |
| `keep_days` | `0` | Delete stored messages older than this. 0 = keep everything. |
| `max_messages_per_chat` | `0` | Keep at most this many of the newest messages per chat. 0 = no limit. |

**The phone decides how much it sends.** The two `request_*` settings are requests,
and the default history can't be made *smaller* than about 3 months this way. To save
space, use the local limits (`keep_days`, `max_messages_per_chat`). These apply on
startup and after each history sync batch.

(Values come from whatsmeow's `DeviceProps.HistorySyncConfig` and mautrix-whatsapp's
documented behavior.)

### `[ui]`

| Key | Default | Meaning |
| --- | --- | --- |
| `inline_images` | `true` | Show the login QR as a small image in terminals with Kitty graphics support (Ghostty, kitty). Requires true color; inside tmux also `allow-passthrough on`. Otherwise text blocks are used regardless. `false` always uses text blocks. |

### `[privacy]`

| Key | Default | Meaning |
| --- | --- | --- |
| `mark_read` | `"local-only"` | What happens when you read a chat in go-whassup. `local-only`: only clear it in go-whassup; **nothing is sent**. `read-receipts-off`: also mark it read on your phone and other devices, via `read-self` receipts like the official apps with *Read receipts* off; senders see no blue ticks. `read-receipts-on`: also send read receipts (blue ticks), like the official apps' default. |

The trade-off with `read-receipts-off`/`read-receipts-on`: each read sends an (end-to-end encrypted)
receipt, so WhatsApp's servers learn *when* you read chats, though not what.

### `[encryption]`

Toggling `enabled` in Settings takes effect **immediately**: stored messages are encrypted
or decrypted in the background while go-whassup keeps working, and the config is saved
only once the change has started (if the key can't be set up, nothing changes). Editing
the file by hand takes effect at the next start of `go-whassup`, `listen` or `login`; `db`
commands never start or reverse encryption. To change `key_source` while encryption is on,
turn encryption off, let it finish, then turn it on with the new source.

With `password-command`, the command runs while the TUI owns the terminal, so it must not
need the terminal itself: GUI pinentries (e.g. `pinentry-mac`) and `pass` with a GUI
agent are fine, a curses pinentry is not.

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Encrypt message text, reactions and attachment keys in `messages.db`. **Encrypts message text only: who you talk to, when, and contact names stay readable on disk.** Message search is off while it's on. Turning it on re-encrypts stored messages; turning it off decrypts them back. The Settings toggle asks first, with its own question each way (encrypt / decrypt at next start). |
| `key_source` | `"keyring"` | `keyring` (OS keychain, no prompt), `password-command` (runs `password_command`; its output is the passphrase), or `prompt` (asks for a passphrase at startup; turning encryption on in Settings asks for the new one there, twice). |
| `password_command` | `""` | Shell command that prints the passphrase, e.g. `pass show whatsapp`. Required with `key_source = password-command` (even while encryption is off), and only listed in Settings then; choosing that source asks for it. |

### `[notifications]`

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | Raise a desktop notification for new messages while the terminal isn't focused (not for muted chats). See `usage.md` › "Desktop notifications". |
| `preview` | `"full"` | What a notification shows: `full` = chat, sender and text; `sender` = who wrote, not what; `none` = only that a message arrived. Your OS may keep notifications in its history. |

### `[media]`

| Key | Default | Meaning |
| --- | --- | --- |
| `save_dir` | `"~/Downloads/go-whassup"` | Where `d` in the attachments view saves a copy. Created `0700`; copies are `0600` and never overwrite (`name (1).ext`). Copies aren't encrypted, even with encryption on. |

## Planned sections

- More `[ui]` settings: theme and keybindings, as the TUI grows.

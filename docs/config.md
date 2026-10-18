# Configuration

`$XDG_CONFIG_HOME/go-whassup/config.toml` (default `~/.config/go-whassup/config.toml`).
The file is optional: if it's missing, the defaults apply. The Settings screen in the
TUI (step 3) will read and write it.

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
- **Adding a setting:**
  1. Add a field with `toml` and `comment` tags.
  2. Set its default in `Default()`.
  3. Add any validation to `Validate()`.
  4. Document it in the table below.
  5. Extend the round-trip test.

## Settings

### `[history]`

| Key | Default | Meaning |
| --- | --- | --- |
| `request_full_sync` | `false` | Ask the phone for about 1 year of history when linking, instead of its default of about 3 months. **Only affects pairing** (`wa-tui login`); to change it later, re-link. |
| `full_sync_days` | `0` | With `request_full_sync`: how many days to ask for (0 = the phone's full-sync default). Invalid without `request_full_sync`, because WhatsApp only reads it alongside the full-sync flag. |
| `keep_days` | `0` | Delete stored messages older than this. 0 = keep everything. |
| `max_messages_per_chat` | `0` | Keep at most this many of the newest messages per chat. 0 = no limit. |

**The phone decides how much it sends.** The two `request_*` settings are requests,
and the default history can't be made *smaller* than about 3 months this way. To save
space, use the local limits (`keep_days`, `max_messages_per_chat`). These apply on
startup and after each history sync batch.

(Values come from whatsmeow's `DeviceProps.HistorySyncConfig` and mautrix-whatsapp's
documented behavior.)

## Planned sections

- `[encryption]`: whether message text is encrypted at rest, and the key source
  (`keyring`, `password_command`, `prompt`). See `storage.md`.
- `[ui]`: theme and keybindings, as the TUI grows.

# Storage

## Locations (XDG on every platform, including macOS)

| Path | Contents |
| --- | --- |
| `$XDG_DATA_HOME/go-whassup/` (default `~/.local/share/go-whassup/`) | Databases. Directory created `0700`. |
| `…/session.db` | whatsmeow's store: device identity, Signal keys, contacts, app state. **Secret.** Created `0600`. Deleting it forces a re-pair; the old entry stays in the phone's Linked devices until you remove it there. |
| `$XDG_STATE_HOME/go-whassup/wa.log` (default `~/.local/state/go-whassup/wa.log`) | Log, `0600`. Message *metadata* at info level; message *text* only with `-debug`. |

Relative `XDG_*` values are ignored, as the XDG spec requires.

## Session DB (whatsmeow `sqlstore`)

- Driver: `modernc.org/sqlite` (pure Go, so `CGO_ENABLED=0` holds). It registers as
  `"sqlite"`, and whatsmeow's dialect parser accepts that name.
- DSN parameters use modernc syntax. The `?_foreign_keys=on` shown in whatsmeow's docs
  is mattn syntax and does nothing here.
  - `_pragma=foreign_keys(1)`
  - `_pragma=journal_mode(WAL)`
  - `_pragma=busy_timeout(5000)`
  - `_txlock=immediate`
- Why these settings: whatsmeow writes from several goroutines. Immediate
  transactions plus WAL plus a busy timeout avoid `SQLITE_BUSY` ("database is
  locked"). We deliberately do **not** use `SetMaxOpenConns(1)`: whatsmeow's
  `DoTxn` helpers can nest, which risks deadlock on a single connection.
- We open the `*sql.DB` ourselves and call `sqlstore.NewWithDB` + `Upgrade`.

## Messages

whatsmeow does **not** persist message content. Our own chat/message schema
(`internal/store`) arrives in step 2.

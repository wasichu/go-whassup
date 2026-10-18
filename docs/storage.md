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

## Messages (step 2): built so encryption can be added later

whatsmeow does **not** persist message content. Our own schema in `internal/store`
stores it. Message text will be plaintext at first, but the design must make
encryption a drop-in later rather than a migration:

- **One codec seam.** A `store.Codec` interface (`Seal([]byte) ([]byte, error)` /
  `Open([]byte) ([]byte, error)`) has a no-op plaintext default. Message text and
  captions pass through it at exactly **one write site and one read site** in
  `internal/store`. No other code touches the stored text column.
- **Per-row marker.** Each row gets a `codec`/`key_id` column (0 = plaintext), so
  plaintext and encrypted rows can coexist and turning encryption on needs no
  migration. Existing rows can be re-sealed in the background later.
- **Metadata stays plaintext:** chat, sender, timestamps, message IDs. Listing and
  sorting keep working in SQL. The trade-off is that metadata (who you talk to and
  when) isn't protected.
- **Search** over encrypted text decrypts in memory, so there's no SQL `LIKE`/FTS on
  sealed rows.
- **Crypto:** AES-GCM from the standard library or XChaCha20-Poly1305 from
  `x/crypto` (already in the module graph). No new dependency for the cipher itself.

### Where the key would come from

This is the real complexity; the cipher is easy. Planned sources, in order of preference:

1. **OS keyring** (macOS Keychain / Secret Service on Linux). No prompt on launch,
   and the key is shielded from other programs running as the same user.
2. **`password_command`**, pianobar-style. The user configures a command, e.g.
   `gpg --decrypt ~/.config/go-whassup/pw.gpg` or `pass show whatsapp`. We run it and
   use its stdout as a passphrase, then derive the key with scrypt or argon2 from
   `x/crypto`. No dependency, and it hands secret storage to the user's existing tools.
3. A passphrase prompt at startup as the fallback. Losing it loses local history,
   not the account; re-pairing re-syncs recent history.

`session.db` can't be protected this way, because whatsmeow owns its tables.
Protecting it would need whole-file encryption, i.e. an encrypting SQLite VFS. That
means a driver change; SQLCipher would need cgo, so it's out. Defer that until
there's a concrete need.

### Why bother: comparison with WhatsApp's own apps

- **Android WhatsApp** keeps chats in plain SQLite (`msgstore.db`) in the app's
  private directory. It relies on the per-app **sandbox** (each app runs as its own
  Linux UID) plus file-based device encryption. It encrypts only data that leaves the
  sandbox: local backups (`.crypt14`/`.crypt15`, key in the app's private `files/key`)
  and, optionally, end-to-end encrypted cloud backups.
- **iOS** works the same way, using the app container plus iOS Data Protection.
- **The desktop has no per-app sandbox for us.** Any process running as the user can
  read `~/.local/share/go-whassup/`. FileVault/LUKS protects only the powered-off
  disk. So "plain DB + OS protection" is **weaker** here than on a phone. That is
  why the codec seam exists and why keyring-backed encryption is a real follow-up,
  not a nice-to-have.

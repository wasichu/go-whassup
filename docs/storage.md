# Storage

## Locations (XDG on every platform, including macOS)

| Path | Contents |
| --- | --- |
| `$XDG_DATA_HOME/go-whassup/` (default `~/.local/share/go-whassup/`) | Databases. Directory created `0700`. |
| `…/messages.db` | **Our** chats, messages and contact names (`internal/store`). Created `0600`. Safe to delete: it refills from live messages and the next history sync (which only happens on re-link). |
| `…/session.db` | whatsmeow's store: device identity, Signal keys, contacts, app state. **Secret.** Created `0600`. Deleting it forces a re-pair; the old entry stays in the phone's Linked devices until you remove it there. |
| `$XDG_STATE_HOME/go-whassup/wa.log` (default `~/.local/state/go-whassup/wa.log`) | Log, `0600`. Message *metadata* at info level; message *text* only with `-debug`. |

Relative `XDG_*` values are ignored, as the XDG spec requires.

## SQLite settings (both databases, `internal/sqlite`)

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
- `internal/sqlite.Open` is the only place that knows the driver and DSN. For the
  session DB we pass its `*sql.DB` to `sqlstore.NewWithDB` + `Upgrade`.
- **Two processes are fine.** Thanks to WAL, `wa-tui db chats` can read while
  `wa-tui listen` writes.

## Messages DB (`internal/store`)

whatsmeow does **not** persist message content, so we do.

**Schema (v2; migrations in `schema.go`, tracked by `PRAGMA user_version`, append-only):**

| Table | Columns |
| --- | --- |
| `chats` | `jid` PK, `name`, `is_group`, `last_message_at` (unix s) |
| `messages` | PK `(chat_jid, id)`, `sender_jid`, `push_name`, `ts` (unix s), `from_me`, `kind`, `body` BLOB, `codec` |
| `contacts` | `jid` PK, `name` (your address-book name for them), `push_name` (the name they chose), `business_name`. Filled by contact snapshots/updates, history sync and incoming messages. v2 added `name` and `business_name`. |

**Behaviour:**
- **Writing.** `store.Recorder` is registered directly with `AddEventHandler`, not through a
  lossy subscription, and writes synchronously.
  - Live messages and `HistorySync` batches are both stored.
  - `INSERT OR IGNORE` on `(chat_jid, id)` dedupes overlapping history and live
    delivery.
  - Reactions are **skipped** for now (they'll attach to their target messages later).
  - Edits and deletes are not applied yet (whatsmeow delivers them as protocol messages,
    which we skip).
- **Names** (`titleSQL`). A group's title is its subject. A person's title, as on the
  phone, is your **address-book name**, then the name history sync gave the chat, then
  their push name, then their business name, then the JID.
  - In groups, each message's `SenderName` resolves the same way per sender.
  - Contact updates are partial upserts: an empty field never erases a known one.
- **Directory** (`Store.Directory`): all chats, then contacts you've never chatted with
  (by name). The TUI's finder searches this.
- **Ordering.** By `(ts, id)`, so messages in the same second stay stable.
- **Retention.** `keep_days` and `max_messages_per_chat` (see `config.md`) are applied by
  `Store.Prune` on startup and after every history sync batch. The per-chat cap uses a
  `row_number()` window.
- **Opening a newer schema** than the build knows is an error, not a silent downgrade.
- **History arrives only when linking.** The phone pushes its history sync right after
  pairing, and whatsmeow doesn't replay it. To backfill an existing install:
  1. Set any `[history] request_*` options.
  2. Run `wa-tui login -relink`, and leave it running until the `history sync` lines
     stop. It unlinks the old session (on the phone too, when reachable) before pairing.

### Built so encryption can be added later

Message text is plaintext today. The design makes encryption a drop-in rather than a
migration (implemented in `internal/store/codec.go`):

- **One codec seam.** The `store.Codec` interface (`ID()`, `Seal`, `Open`) has a no-op
  `Plaintext` default (ID 0). Message text and
  captions pass through it at exactly **one write site and one read site** in
  `internal/store`. No other code touches the stored text column.
- **Per-row marker.** The `messages.codec` column records which codec sealed each row,
  so plaintext and encrypted rows coexist and turning encryption on needs no migration.
  A row sealed by a codec the store doesn't have fails loudly instead of returning
  garbage, and a test with a stand-in codec proves this. Existing rows can be re-sealed
  in the background later.
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

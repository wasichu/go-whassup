# Storage

## Locations (XDG on every platform, including macOS)

| Path | Contents |
| --- | --- |
| `$XDG_DATA_HOME/go-whassup/` (default `~/.local/share/go-whassup/`) | Databases. Directory created `0700`. |
| `…/messages.db` | **Our** chats, messages and contact names (`internal/store`). Created `0600`. Safe to delete: it refills from live messages and the next history sync (which only happens on re-link). |
| `…/media/` | Downloaded attachments: directory `0700`, files `0600`, named by a hash of chat+message ID. **Plaintext on disk** (see below). |
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

**Schema (v3; migrations in `schema.go`, tracked by `PRAGMA user_version`, append-only):**

| Table | Columns |
| --- | --- |
| `chats` | `jid` PK, `name`, `is_group`, `last_message_at`, `last_read_at` (unix s; v3; chats existing at upgrade count as read) |
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
- **Read state.** Unread = incoming messages newer than `last_read_at`.
  - `MarkRead` only moves forward. It's used for local reads, for replies (replying
    reads the chat), and for `ChatRead` events from other devices.
  - History sync's per-chat unread count is applied with `SetUnreadFromPhone`, which
    may move backwards, because the phone is authoritative at link time.
- **Search index** (`search.go`). It's maintained in Go, in the same transaction as each
  message write, edit, delete and prune, because triggers can't see through the codec.
  - It's only written while the codec is plaintext, and `Search` returns
    `ErrSearchUnavailable` otherwise.
  - **Turning encryption on must empty `messages_fts` and `VACUUM`**, because the index
    is a plaintext copy of the words.
  - Migration 8 indexed the existing plaintext rows.
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
- **Turning encryption on re-encrypts existing rows** (decided 2026-10-18). The per-row
  `codec` column makes this resumable. **Message-text search is unavailable while
  encryption is on**; the UI says so rather than searching partially. Finding chats and
  contacts by name still works, because names aren't encrypted.
- **Search is always local.** WhatsApp is end-to-end encrypted, so its servers can't
  search messages and offer no search API; the official apps search their on-device
  copies too. Ours covers what's in `messages.db`. Message search would use SQLite FTS5,
  whose index is itself a plaintext copy of the words, hence the trade-off above.
- **Crypto:** AES-GCM from the standard library or XChaCha20-Poly1305 from
  `x/crypto` (already in the module graph). No new dependency for the cipher itself.

### Encryption decisions (2026-10-18)

- **Turning it on** re-encrypts existing rows (resumable, in the background, with
  progress shown). New messages are sealed immediately.
- **Turning it off** is allowed, after a confirmation. It decrypts all rows back to
  plaintext.
- **Key unavailable at startup** (keyring entry missing, wrong passphrase, failing
  `password_command`):
  1. refuse to open messages and ask for the key;
  2. the only way past that is an explicit, confirmed **reset**: delete `messages.db`
     and re-link to backfill. History older than the phone's sync is lost; the confirm
     says so.
- **Logging:** while encryption is on, message text is **never** logged, even with
  `-debug`.
- **Metadata stays plaintext:** who you talk to and when, chat/contact names, and
  `session.db`. The TUI must say this **wherever encryption is offered or enabled**:
  - in the Settings description;
  - in the enable confirmation;
  - on the unlock screen's help line.

  The wording: "Encrypts message text only. Who you talk to, when, and contact names
  stay readable on disk."
- **Technical defaults:**
  - Envelope keys: a random data key in a `keys` table, wrapped by the keyring or
    passphrase key, so a passphrase change rewraps one key.
  - AES-256-GCM (stdlib).
  - Argon2id for passphrases (`x/crypto`), salt stored with the wrapped key.
  - `secure_delete` plus `VACUUM` and a WAL checkpoint after re-encryption, so old
    plaintext pages are really gone.
  - Unlock screen in the TUI; `listen` and `db` prompt or use `password_command` /
    keyring.

### Encryption as built (2026-10-18)

**Code.**
- `internal/crypt`: AES-256-GCM with associated data, envelope wrapping, and Argon2id.
- `internal/keysource`: keyring, command and prompt.
- `internal/store/keys.go`: the keys table and the re-seal.
- `internal/app/encryption.go`: the startup sequence.

**What's encrypted.**
- Message text, **reactions** (they're content: what people thought of a message), and
  attachment keys.
- Each value is bound to its message and column, and for reactions to the sender, via
  GCM associated data, so values can't be swapped between rows.
- The per-row `codec` columns hold the **key ID** (0 = plaintext), and the algorithm is
  recorded on the `keys` row.

**What stays readable.**
- Chat and sender IDs, timestamps, message kinds, push names, contact and group names.
- `session.db`.
- WhatsApp's own Signal keys.

**Keys.**
- A random data key is stored wrapped in `keys`.
- `keyring`: the key-encryption key is a random 32 bytes in the macOS Keychain (via
  `/usr/bin/security`, secret on stdin; the Keychain ACL names `security`, so rebuilds
  don't prompt) or Secret Service (`secret-tool`).
- `password-command` / `prompt`: Argon2id (3 passes, 64 MiB, 4 threads; parameters stored
  with the key).
- `password_command` is refused if the config file or its directory is writable by
  others.

**Enabling.**
1. Delete the plaintext media cache.
2. Store the key; the same transaction empties the search index, so an interrupted
   enable never leaves a plaintext index.
3. Switch writes to the key.
4. Re-seal in batches of 500 in the background, checkpointing the WAL after every batch.
5. `VACUUM`.

It resumes on the next start if interrupted. A value that can't be opened stops
re-sealing (`ResealError`) rather than retrying forever.

**Disabling.**
1. Write plaintext.
2. Re-seal back.
3. Rebuild the search index.
4. Delete the key row and `VACUUM`.

The keychain entry is left in place; remove it in Keychain Access if you like.

**While encrypted (or still decrypting).**
- Message text is never logged, even with `-debug`, and whatsmeow's logger is capped at
  info.
- Downloads go to a temporary directory removed on exit. A viewer that copied a file may
  keep its own copy.

**Unlock failure.**
- `wa-tui` refuses to start and explains why: a wrong passphrase or keychain secret, or a
  missing keychain entry.
- The way out is `wa-tui db reset` (type `yes`): it deletes `messages.db` and the media
  cache, keeps the session, and warns that history older than the phone's re-sync is
  lost.

**Deviation from the decision record (2026-10-18).** The passphrase prompt happens in
the terminal **before** the TUI starts, not on an unlock screen inside it. That's one
code path for the TUI, `listen` and `db`, and Bubble Tea doesn't own the terminal yet.
An in-TUI unlock screen is a roadmap follow-up.

**Not yet.**
- Rotating the data key.
- Rewrapping when `key_source` changes (for now: turn it off, then on).

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

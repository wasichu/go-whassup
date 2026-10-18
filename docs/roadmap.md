# Roadmap

Each step is a thin vertical slice that runs end to end. Update this file when a
step lands or the plan changes.

## Done

1. **Scaffold:** mise-pinned toolchain, Justfile, lint rules enforcing the AGENTS.md
   invariants, graft + gopls MCP.
2. **Headless pairing and listening:** phone-code pairing (`wa-tui login`),
   `wa-tui listen`. Verified live on 2026-10-18.
3. **Storage (step 2)**, verified live on 2026-10-18, including a fresh re-link with history backfill:
   - `messages.db` with an encryption-ready codec seam;
   - recording of live messages and history sync, with privacy-ID-stable keys;
   - retention limits;
   - typed TOML config (`[history]`);
   - `wa-tui db chats` / `db messages`;
   - `wa-tui login -relink` to unlink and pair again.

## Done: step 3, minimal TUI (verified live 2026-10-18)

- [x] Sending: `wa.SendText` (emitted as an event); push name announced as
  "unavailable" on connect.
- [x] Chat list | message pane | input; send text; works offline from the store;
  connection state in the status line. Verified live.
- [x] Keymap with generated hints and a `?` overlay.
- [x] Login screen: phone code, QR choice (`rsc.io/qr`, half-blocks), Re-link, esc to
  cancel. Verified live: TUI re-link and QR scan.
- [x] Inline QR image via Kitty graphics (Ghostty/kitty, including through tmux with
  passthrough), text-block fallback. Verified live in Ghostty + tmux 3.8.
- [x] Settings screen: generic over `Config.Fields()`, validated, saved atomically and
  applied live. *(Live test pending.)*
- **Login screen:**
  - Phone-code pairing (the step-1 flow) by default.
  - When already paired: a **"Re-link"** option, the same as `login -relink` via
    `wa.Client.Logout`. Explain that it removes the device from the phone and that
    stored messages are kept.
  - **QR pairing as a choice on the login screen.** It needs a QR encoder
    (`rsc.io/qr` or `skip2/go-qrcode`, both small with no dependencies of their own),
    compiled in. Draw it with Unicode half-blocks, forced black-on-white, and show a
    "terminal too small" fallback.
  - `wa.Client` gains `PairQR(ctx)`, forwarding `QRChannelEventCode` items. Success and
    failure reuse `PairSuccess` / `PairFailed` (see `pairing.md`).
- **Settings screen** (opened with `,`):
  - Lists `config.Config` grouped by section, validates as you edit, and saves with
    `config.Save`.
  - Changes apply through `Update()`.
  - Settings that only matter at pairing time are labelled as such.

## After step 3: features (in this order)

1. [x] **Contact names** from the phone's address book (whatsmeow's contact store), so
   the chat list shows real names. Also **contact search / new chat** (`/`): fuzzy-find
   any contact and message someone you haven't chatted with yet. *(Live test pending.)*
2. [x] **Unread counts and new-message markers**: badges, a "N new" divider, read after
   dwell while focused, `[privacy] mark_read` = local-only/read-receipts-off/read-receipts-on. *(Live test pending.)*
3. [x] **Edits, deletes and reactions** applied to stored messages and shown in the
   TUI. *(Live test pending.)* Follow-up: sending reactions/edits/deletes from the TUI.
4. [x] **Media:**
   - download and open attachments (`o`), with an extension allowlist;
   - send files (`a`): photos for JPEG/PNG/WebP, documents otherwise;
   - a cache with cleanup on delete and prune;
   - media keys sealed through the codec.

   *(Live test pending.)* Follow-ups:
   - video/audio sent as native media (needs duration/waveform);
   - a policy for the media cache under encryption;
   - on macOS, setting the quarantine attribute on downloads;
   - inline image previews via Kitty graphics.
5. [x] **Groups:** names and topics from group info, participants with admins (`i`),
   member counts, `@mentions` shown as names (and `@you`). *(Live test pending.)*
   Follow-up: typing mentions when sending (autocomplete).
6. [x] **Message search** (`s`, `wa-tui db search`): local SQLite FTS5, accent-insensitive,
   prefix on the last word, jump-to-message. Unavailable while encryption is on (the
   index is plaintext). *(Live test pending.)*
7. **Encryption at rest** via the `store.Codec` seam:
   - an `[encryption]` config section (on/off, key source);
   - key sources in order: OS keyring, then a pianobar-style `password_command`, then a
     prompt;
   - decisions recorded in `storage.md` › "Encryption decisions":
     - on re-encrypts existing rows;
     - off decrypts, after a confirmation;
     - a missing key blocks, with a confirmed reset as the escape hatch;
     - message text is never logged while encrypted;
     - a metadata warning wherever encryption is offered.
8. **Desktop notifications.** (Read receipts shipped as `mark_read = "read-receipts-on"`, opt-in.)
9. Surface the "code rejected" pairing failure immediately (needs a whatsmeow hook;
   see `pairing.md`).

## Last, before going public

- **LICENSE: MPL-2.0**, matching whatsmeow (decided 2026-10-18).
- **README for users:** what it is, an "unofficial; against WhatsApp's terms" warning,
  install steps, a screenshot. AGENTS.md and `docs/` stay contributor-facing.
- **CI:** GitHub Actions running `just check` via the mise action, so CI uses the same
  pinned tools.
- **Release binaries:** cgo-free cross-compiles for darwin/linux/windows ×
  amd64/arm64, with the version stamped via `-ldflags -X`, from a `just release` recipe
  (no GoReleaser unless needed).
- **Dependency bumps:** Renovate/Dependabot for whatsmeow's pseudo-versions, gated by CI.

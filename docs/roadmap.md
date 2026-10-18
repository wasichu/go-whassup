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

1. **Contact names** from the phone's address book (whatsmeow's contact store), so the
   chat list shows real names. Also **contact search / new chat**: fuzzy-find any
   contact and message someone you haven't chatted with yet.
2. **Unread counts and new-message markers** in the chat list.
3. **Edits, deletes and reactions** applied to stored messages.
4. **Media:** download images and documents; open them with `open` / `xdg-open`; and
   **send files** (pick a path, send as a document or image).
5. **Groups:** names from group info, participants, mentions.
6. **Search.** Unavailable while encryption is on (decided); the UI says so.
7. **Encryption at rest** via the `store.Codec` seam:
   - an `[encryption]` config section (on/off, key source);
   - key sources in order: OS keyring, then a pianobar-style `password_command`, then a
     prompt;
   - turning it on re-encrypts existing rows (decided);
   - see `storage.md`.
8. **Read receipts** (opt-in; never automatic, because of ban risk) and **desktop
   notifications**.
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

# Roadmap

Each step is a thin vertical slice that runs end to end. Update this file when a
step lands or the plan changes.

## Done

1. **Scaffold:** mise-pinned toolchain, Justfile, lint rules enforcing the AGENTS.md
   invariants, graft + gopls MCP.
2. **Headless pairing and listening:** phone-code pairing (`wa-tui login`),
   `wa-tui listen`. Verified live on 2026-10-18.
3. **Storage (step 2):**
   - `messages.db` with an encryption-ready codec seam;
   - recording of live messages and history sync, with privacy-ID-stable keys;
   - retention limits;
   - typed TOML config (`[history]`);
   - `wa-tui db chats` / `db messages`.

## Next: step 3, minimal TUI

- Chat list | message pane | input; send text.
- Events arrive through `wa.Subscribe` + a `tea.Cmd`; reads come from `store`.
- **Login screen:**
  - Phone-code pairing (the step-1 flow) by default.
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

## Later

- **Encryption at rest** for message text, via the `store.Codec` seam:
  - an `[encryption]` config section (on/off, key source);
  - key sources in order: OS keyring, then a pianobar-style `password_command`, then a
    prompt;
  - see `storage.md`.
- Edits, deletes and reactions applied to stored messages.
- Groups: names from group info, participants.
- Read receipts (opt-in; never automatic, because of ban risk), media download/open,
  desktop notifications, search.
- Surface the "code rejected" pairing failure immediately (needs a whatsmeow
  hook; see `pairing.md`).

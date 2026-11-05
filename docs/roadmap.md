# Roadmap

The everyday messaging feature set is implemented. Focus on reliability,
maintenance, and friction found during normal use; there is no committed queue
of additional messaging features.

## Current focus

- Maintain compatibility with WhatsApp and review automated dependency updates.
- Fix issues found in normal use; no additional messaging feature is scheduled.
- Keep release distribution routine: Homebrew discovers published stable releases
  hourly and validates the formula/bottles before publication. See `homebrew.md`.

## Verification follow-ups

These are account/device checks, not known bugs or missing implementations.
Automated tests run offline and cannot establish every phone or audio-device
behavior.

- **Mentions:** basic group autocomplete/sending was confirmed live on 2026-11-04.
  Replies, edits, and phone notification behavior remain useful checks.
- **Voice:** phone-note playback and TUI recording/sending were confirmed live on
  macOS on 2026-11-04. Check Linux PulseAudio/PipeWire-Pulse, microphone permission
  failures, and device disconnection. See `voice.md`.
- **Encryption:** verify enabling/disabling, unlocking, and interrupted conversion
  with disposable data before relying on it for a real archive. Existing behavior
  and limitations are documented in `storage.md` and `config.md`.

## Optional ideas

These need a concrete use case or an explicit scope decision before implementation.
They are not promised work or prerequisites for using the app.

- **Encryption usability and key management:** an in-TUI unlock screen, data-key
  rotation, rewrapping when `key_source` changes, and optional keychain cleanup.
  Encryption at rest already exists. Downloads while encrypted use a temporary
  cache removed on exit; the cache policy is implemented, not an open design task.
- **Native video/audio file sending:** ordinary files currently send as photos or
  documents; native voice notes are implemented separately.
- **Editing replies and attachment captions.** Ordinary text edits are implemented.
- **macOS download quarantine** as additional download hardening.

## Shipped

| Area | Implemented behavior |
| --- | --- |
| Pairing | Phone code, QR, re-link, immediate pairing errors, session persistence |
| Local history | SQLite storage, history sync, retention, contact names, offline browsing and CLI inspection |
| Messaging | Text, replies, reactions/emoji search, edits, delete for everyone, unread counts, optional read receipts |
| Groups | Names/topics, member/admin lists, communities, sender/reaction info, incoming and outgoing mentions |
| Media | Download/open/save, file sending, file browser/completion, inline image and QR previews in compatible terminals, cache cleanup |
| Search | Local full-text search, accent-insensitive matching, jump to message; unavailable while encryption is on |
| Privacy | Optional encryption at rest, keychain/password-command/passphrase sources, live conversion, temporary media cache while encrypted |
| Notifications | Terminal desktop notifications, preview controls, muted-chat handling |
| TUI | Context hints, searchable Ctrl+P actions, scrollable F1 help, settings, fictional demo |
| Distribution | GitHub releases, cgo-free macOS/Linux binaries on amd64/arm64, Homebrew tap, bottles, and automated stable-release handoff |
| Maintenance | Pinned tooling, Linux/macOS CI, weekly vulnerability scans, whatsmeow updater, Renovate for other updates |

Recent milestones:

- **v0.4.1:** group mention autocomplete with readable draft names and real
  WhatsApp mention metadata, including replies and edits.
- **v0.4.0:** voice playback and recording/preview/send/discard, Enter to open a
  selected attachment, compact hints, Ctrl+P actions, and F1 help.
- **v0.3.0:** editing and deleting your own messages.
- **v0.2.x:** replies, reactions, emoji search, and sender/reaction details.
- **v0.1.0:** initial public release and Homebrew installation.

See GitHub release notes for version details. Keep implementation explanations in
`architecture.md`, `storage.md`, `pairing.md`, and `voice.md` rather than retaining
completed design plans here.

## Project boundaries

- One binary, `CGO_ENABLED=0`, minimal dependencies; no additional app service.
- macOS and Linux releases; Windows users use the Linux build in WSL.
- Our own Homebrew tap; homebrew-core submission is not planned.
- Dependency updates require review; publishing our own tested release formula
  is a separate automation policy.

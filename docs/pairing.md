# Pairing

go-whassup links as a WhatsApp *linked device*, the same mechanism WhatsApp Web
uses. We support **phone-number code pairing** only; QR pairing may come later (see below).

## Sequence (whatsmeow ordering constraints)

`wa.WhatsApp.Pair` owns the whole sequence because the steps must happen in this
order:

1. `GetQRChannel(ctx)`: must be called **before** connecting.
2. `ConnectContext(ctx)`: opens the login websocket.
3. Wait for the **first QR channel item**. It shows the socket is ready.
   `PairPhone` fails if called earlier.
4. `PairPhone(...)` returns the 8-character code the user types on their phone.
5. A goroutine keeps draining the QR channel. Timeouts and errors become `PairFailed`.
   Success arrives as the `PairSuccess` event.
6. After `PairSuccess` the server forces a **reconnect**. Wait for `Connected` before
   doing anything.
7. Stay connected for a while. Right after linking, the phone pushes app-state keys
   and history sync. `wa-tui login` therefore stays online until Ctrl-C instead of
   exiting at `Connected`.

The `ctx` passed to `Pair`/`Connect` bounds the **connection's lifetime**, including
whatsmeow's automatic reconnects, not just the dial. Don't pass a short timeout
context; use a separate timer for "how long to wait for the user". The login
websocket gives up after about 160 s of unanswered pairing, and our wait is 3 min.

## QR pairing (`PairQR`) and cancelling

- `PairQR` runs the same start sequence (`startPairing`: QR channel, then connect) and
  emits a `PairQRCode{Code}` event for each code, through the normal event path. Codes
  rotate about every 20 s; the first lasts about 60 s. QR pairing has no display-name
  validation, and the phone still lists us as "go-whassup" via `DeviceProps`.
- **Two contexts.** The QR channel gets a child context that we can cancel ourselves;
  `ConnectContext` gets the caller's long-lived context.
  - `CancelPairing()` cancels the child context. That is whatsmeow's designed cancel
    path: it closes the channel, removes its handler and disconnects. It emits no
    `PairFailed`.
  - After success the channel is already closed, so a late cancel can't drop the new
    connection.
  - Plain `Disconnect()` is *not* a cancel: the QR emitter would keep running.
- Retrying after a cancel is allowed. `GetQRChannel` only refuses when already
  connected or when a session exists.
- **History-request options** (`request_full_sync`, `full_sync_days`) are written into
  whatsmeow's process-wide `DeviceProps` at the start of each pairing, from
  `WhatsApp.SetOptions`. So Settings changes apply to the next link without a restart.
- **Not supported:** WhatsApp's newer *passkey* pairing prompt (whatsmeow emits
  `passkey-request` items). If it appears, pairing reports it as a failure.

## Server-side restrictions on how we identify

There are two separate identities, and they have different rules:

| What | Where it's set | Rule |
| --- | --- | --- |
| **Pairing display name** | `PairPhone(..., clientType, clientDisplayName)` | WhatsApp validates this server-side. It must look like `Browser (OS)` using **common** browser/OS names, or the request fails with HTTP 400. We send `PairClientChrome` + `"Chrome (Linux)"`. |
| **Device name** (shown in the phone's *Linked devices*) | `store.SetOSInfo("go-whassup", …)` before pairing | Free-form; this is how our session shows up to the user. |

In other words, every unofficial linked-device client has to claim to be a
mainstream browser during pairing. WhatsApp only expects WhatsApp Web and desktop
apps to link this way.

For comparison, waha-tui doesn't deal with this directly. It calls the WAHA
server's `auth/request-code` endpoint, and the WAHA *engine* does the pairing:
- `WEBJS` really is a Chromium browser running WhatsApp Web.
- `NOWEB` (Baileys) and `GOWS` (whatsmeow) are browserless and face the same rule we do.

## Re-linking (`wa-tui login -relink`)

`wa.Client.Logout` resets to a fresh, unpaired device so `Pair` can run again. Use it
for a history backfill, a stuck session, or switching accounts.
1. Unlink on WhatsApp's side. Connect if needed, wait up to 15 s to authenticate, then
   whatsmeow's `Logout` sends `remove-companion-device`. This also removes the entry
   from the phone's *Linked devices*.
2. If that fails (offline, or the phone already unlinked us), **delete the local
   session anyway** and report `phoneUnlinked = false`. The user then removes the stale
   entry on the phone themselves.
3. Rebuild the whatsmeow client on `container.NewDevice()`. After `Store.Delete`, the
   old client's stores are no-ops and can't be reused.

`messages.db` is untouched, and messages already stored dedupe against the new
history sync. The TUI's login screen will offer the same option (see `roadmap.md`).

## Phone number format

`wa.NormalizePhone` strips `+`, spaces, dashes, dots and parentheses, then requires
8–15 digits with no leading `0`. That means international format with the country code:
`+1 (555) 123-4567` → `15551234567`. Quote numbers containing spaces on the
command line.


## What a successful pairing looks like (log, info level)

```
Successfully paired 1555…:4@s.whatsapp.net   # PairSuccess (":4" = our device number)
Got 515 code, reconnecting...                 # forced reconnect, expected
Successfully authenticated                    # Connected
Uploading 812 new prekeys to server
Updating contact store with N push names from history sync
Stored N message secret keys from history sync
```

`Error sending close to websocket ... EOF` warnings around reconnects are harmless.

## Troubleshooting

**Phone says "check that your phone number is correct" and the log shows**
`Failed to handle code pair notification: missing <link_code_pairing_wrapped_primary_ephemeral_pub>`

The phone answered our pairing request (the pairing ref matched) but sent a
rejection instead of its half of the handshake. Seen once in practice (2026-10-18).
An immediate retry with a fresh code succeeded, so the cause wasn't confirmed. Likely
causes: the code was entered in an app registered to a different number (e.g. WhatsApp
Business), a mistyped or stale code, or a transient server issue. Retry with a new code,
spaced a few minutes apart, because pairing requests are rate-limited (`ErrIQRateOverLimit`).

**Known gap:** whatsmeow only *logs* this failure and emits no event, so `login`
keeps waiting until the server closes the login socket (~160 s) or our 3-minute
timer fires. Surfacing it right away would need a whatsmeow hook or patch.

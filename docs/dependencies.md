# Dependencies

The policy (stdlib first, a reason for each new module, cgo-free) is in
`AGENTS.md`. This doc is about keeping the one dependency current that has to be.

## whatsmeow: a weekly bump pull request

WhatsApp changes its protocol on its own schedule, and whatsmeow follows with
fixes, including the client version it reports. A client that falls far behind
can stop connecting. whatsmeow has no tagged releases (`go.mod` pins a
pseudo-version, a commit on its `main`), so version-based tools like Dependabot
don't fit. Instead, `.github/workflows/bump-whatsmeow.yml`:

1. Runs every Monday, or on demand (Actions → bump-whatsmeow → Run workflow, or
   `gh workflow run bump-whatsmeow.yml`).
2. Runs `go get go.mau.fi/whatsmeow@main` and `go mod tidy`. If nothing changed, it
   stops.
3. Runs `just check` on Linux, then force-pushes the `deps/whatsmeow` branch (main
   plus one commit) and opens a pull request, or updates the open one. The pull
   request links whatsmeow's changes and says whether the check passed; a failing
   update still gets a pull request, since it means our code needs adjusting.
4. Starts the `check` workflow (Linux and macOS) on the branch. Pull requests opened
   by a workflow's token don't trigger other workflows, but a dispatched run does,
   and its checks show on the pull request.

Merging is by hand, after reading what changed. A bump that changes behavior
(new events, renamed APIs) gets the usual doc updates in the same pull request.

**Setup:** the repo must allow Actions to open pull requests (Settings → Actions →
General → "Allow GitHub Actions to create and approve pull requests").

Other modules (Bubble Tea, lipgloss, SQLite, TOML) are stable and bumped by hand
when there's a reason; run gopls `go_vulncheck` after any bump.

# Dependencies

Keep the application cgo-free and dependencies minimal. `AGENTS.md` contains the
coding policy; `mise.toml` pins development tools independently of the minimum Go
language version in `go.mod`.

## Update ownership

| Component | Updater | Review |
| --- | --- | --- |
| whatsmeow | `bump-whatsmeow` GitHub Actions workflow, Monday 14:17 UTC or on demand | One PR on `deps/whatsmeow`, including upstream comparison and validation output |
| Other Go modules | Renovate `gomod` manager | Weekly PRs; major updates need Dependency Dashboard approval before creation |
| GitHub Actions | Renovate `github-actions` manager | Weekly PRs |
| Pinned development tools | Renovate `mise` manager | Weekly PRs against `mise.toml` |

Renovate's window is Monday before 06:00 America/Chicago. It runs `go mod tidy`
after module updates. It does not separately update whatsmeow, avoiding competing
PRs for the same dependency. Workflow actions are pinned to immutable commits;
Renovate updates those pins and their version comments. It also leaves the Go
language/toolchain directives in `go.mod` alone: `mise.toml` owns the toolchain;
raise the minimum only when needed. All updates require review and a manual
merge. Preserve Bubble Tea v2,
pure-Go SQLite, and `CGO_ENABLED=0` when reviewing migrations.

Renovate can discover untagged modules through Go proxy pseudo-versions, but the
custom whatsmeow workflow deliberately follows `main`: WhatsApp protocol fixes
and client-version changes can be needed promptly.

**Setup:** install/enable the [Renovate GitHub App](https://github.com/apps/renovate)
for this repository and complete onboarding if requested. Merely having
`renovate.json` does not run Renovate. Check its Dependency Dashboard and PRs to
verify it is active. The whatsmeow workflow additionally requires Settings →
Actions → General → “Allow GitHub Actions to create and approve pull requests”.

In the Mend Developer Platform, check the repository's Settings → Dependencies:
Silent mode collects proposed updates without automatically opening PRs. For the
weekly PR policy above, turn Silent mode off and enable Automated PRs for this
repository. PR creation is separate from merging; `automerge` remains disabled.

## whatsmeow workflow

`.github/workflows/bump-whatsmeow.yml` runs weekly, or via Actions →
bump-whatsmeow → Run workflow (`gh workflow run bump-whatsmeow.yml`). It:

1. Updates whatsmeow from `main` and tidies the module files. If its version is
   unchanged, it stops.
2. Runs `just check` and `just vulncheck` on Linux. A validation failure still
   opens/updates the PR with the last log lines, so a breaking update is visible.
   An update/download failure stops the workflow and needs investigation.
3. Recreates `deps/whatsmeow` from the default branch plus one dependency commit,
   then opens or updates its PR with a link to upstream changes.
4. Dispatches `check.yml` on that branch for Linux and macOS validation. Bot-token
   PR workflows can require approval, so explicit dispatch keeps validation
   running. Dispatched checks do not automatically populate the PR check rollup;
   a separate job publishes `check / dispatched` on the exact checked commit.
   Only that status-reporting job has permission to write commit statuses.

Do not put manual work on `deps/whatsmeow`: the updater force-pushes that branch.
Review upstream changes, the latest commit's status, and both platform logs before
merging. Behavioral changes or renamed APIs need the usual code and doc updates.

## Local maintenance and vulnerability checks

- `just outdated` lists available module versions without editing module files.
- Update the intended module(s) through `mise exec -- go get …`, staying within
  the supported stack. Avoid unrelated major migrations.
- Run `just tidy`, `just check`, and `just vulncheck` after dependency changes.
- Inspect the `go.mod`/`go.sum` diff for unexpected modules, Go directive changes,
  and new platform or cgo requirements.

`just vulncheck` uses the official Go scanner, pinned through mise, to report
known vulnerabilities reachable from our code. It needs network access to the Go
vulnerability database; it is separate from the offline test suite and local
`just check` gate. CI runs it alongside `just check` on Linux and macOS, and
`.github/workflows/vulncheck.yml` repeats it every Monday (14:43 UTC) even without
source changes. Both workflows support manual runs. Review failed scans and
update affected dependencies or the Go toolchain; do not suppress findings just
to make an update pass.

After changing workflows or Renovate configuration, validate them with actionlint
and Renovate's config validator as well as the normal project checks. Scheduled
jobs and configuration changes only take effect after reaching the default
branch. GitHub can disable scheduled workflows in inactive public repositories;
inspect the Actions page if expected weekly runs stop.

# Homebrew distribution

Install from our own tap:

```sh
brew install wasichu/tap/go-whassup
brew update && brew upgrade wasichu/tap/go-whassup
```

The canonical formula lives in
[wasichu/homebrew-tap](https://github.com/wasichu/homebrew-tap/blob/main/Formula/go-whassup.rb).
It builds the tagged source with `CGO_ENABLED=0`, stamps `-version`, and tests that
version without a WhatsApp account. Successful tap CI builds bottles for supported
runners. Intel macOS may fall back to a source build when Homebrew cannot provide
compatible dependency bottles; GitHub releases also contain an Intel macOS binary.
Homebrew-core submission is not planned.

## Automated release handoff

The tap's `autobump.yml` workflow runs hourly, discovers versions with `brew bump`,
and completes validation/publication for go-whassup. The activation change is
[tap PR #6](https://github.com/wasichu/homebrew-tap/pull/6). Future changes to this
pipeline must reach the tap's `main` branch; editing the app repository alone
does not change the active automation.

1. Publish a stable GitHub release such as `v0.4.2` in `wasichu/go-whassup`.
   A tag alone, draft, or prerelease is not sufficient for automatic publication.
2. The next hourly tap run opens the formula bump. Scheduled runs can be delayed;
   `gh workflow run autobump.yml --repo wasichu/homebrew-tap` runs it on demand.
3. The handoff accepts only a same-repository, bot-created PR on the expected
   release branch, changing only `Formula/go-whassup.rb`. It independently hashes
   the published source archive and permits only the stable URL/checksum update
   and removal of the old bottle/revision information. Changes to build logic,
   dependencies, other formulae, or workflows require review.
4. It dispatches `tests.yml` on that exact PR commit and explicitly builds
   go-whassup on the tap's normal matrix. This avoids relying on a bot-created
   PR's approval-gated workflow. Bottles are uploaded as normal test artifacts.
5. After all jobs succeed, it checks the PR head, release status, and base formula
   again, then dispatches `publish.yml` with the expected head SHA. The existing
   `brew pr-pull` workflow publishes bottles/provenance and updates the formula
   on `main`. Publication is serialized.

This runs with the tap's own `GITHUB_TOKEN`; no new cross-repository PAT or app
secret is required. Bot-triggered workflow limitations are documented in
[GitHub's token guide](https://docs.github.com/en/actions/concepts/security/github_token).
Formula/bottle publication follows
[Homebrew's tap workflow](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap).

This policy automates distribution of our own published release. It does not
approve dependency updates or arbitrary tap PRs. Other formulae retain the
existing bump/review process.

## Validation and recovery

The handoff's policy tests run offline with Python's standard library. The
already-current path is also safe to run against the live repository: it reads
the current formula/release and exits without opening PRs or publishing.
The first future version bump must still exercise the complete new handoff.

Failed checks stop publication. The job reuses an existing dispatch for the same
commit instead of repeatedly rebuilding a failed release every hour. Inspect the
failed run, fix the PR if necessary, and rerun the failed jobs before retrying
`autobump.yml`. A changed PR head or base formula requires fresh verification.

For a manual release or recovery, open the formula URL/checksum bump, let the
normal PR checks build bottles, and dispatch the existing publisher:

```sh
gh workflow run publish.yml --repo wasichu/homebrew-tap \
  -f pull_request=NUMBER -f head_sha=EXPECTED_COMMIT
```

Use the bottle publisher rather than merging a formula PR directly if bottles
are expected. The tested head SHA protects against publishing a later untested
commit. Inspect the formula on `main` and the tap's `go-whassup-VERSION` release
assets afterward.

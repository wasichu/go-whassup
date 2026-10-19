# Homebrew (plan)

Goal: `brew install wasichu/tap/go-whassup` for macOS (and Linux) users, from our own
tap. Decided 2026-10-18: **our tap only**; homebrew-core is not planned (see below). Not
started; it needs the repo to be public and a tagged release first.

Checked against Homebrew's docs on 2026-10-18:
[Acceptable Formulae](https://docs.brew.sh/Acceptable-Formulae) and
[How to Create and Maintain a Tap](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap).

## 1. Our own tap (first)

- A repo named `wasichu/homebrew-tap` (the `homebrew-` prefix lets `brew tap` use the
  short name). `brew tap-new wasichu/tap` scaffolds it, including GitHub Actions that
  build bottles (prebuilt binaries) for each formula change.
- Users then run `brew install wasichu/tap/go-whassup`. No separate `brew tap` is
  needed.
- The formula builds from the tagged source tarball, so it works anywhere Homebrew's Go
  does. Bottles make installs fast.
- Test locally before pushing:
  `brew install --build-from-source wasichu/tap/go-whassup`, then `brew test` and
  `brew audit --strict`.

Draft formula (`Formula/go-whassup.rb`; untested until a tarball exists):

```ruby
class GoWhassup < Formula
  desc "WhatsApp client for the terminal"
  homepage "https://github.com/wasichu/go-whassup"
  url "https://github.com/wasichu/go-whassup/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "<sha256 of that tarball>"
  license "MPL-2.0"
  head "https://github.com/wasichu/go-whassup.git", branch: "main"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "0" # as everywhere else: pure-Go SQLite, static binary
    ldflags = "-s -w -X github.com/wasichu/go-whassup/internal/app.Version=v#{version}"
    system "go", "build", *std_go_args(ldflags:), "./cmd/go-whassup"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/go-whassup -version")
  end
end
```

The test uses `-version` because everything else needs either a linked account or a
terminal (`go-whassup demo` is interactive).

## 2. Keep it current

- On each release tag, a workflow step in this repo opens a PR against the tap with
  `brew bump-formula-pr`, updating the URL and sha256. The tap's own Actions then build
  bottles for that PR.
- Until then, the bump is one manual `brew bump-formula-pr` per release.

## 3. homebrew-core: not planned

Kept for the record, in case it's ever revisited.

What core requires, and where we stand:

| Requirement | Status |
| --- | --- |
| Open-source license compatible with the DFSG | MPL-2.0: yes |
| Builds from source (binary-only belongs in a cask) | yes |
| A stable, immutable tagged release, verified by sha256 | after the first tag |
| No self-update | none |
| Notability | not yet; the docs give no number, so it's reviewers' judgement |

Open question before submitting: go-whassup is an unofficial client, and using it is
against WhatsApp's terms. The Acceptable Formulae page doesn't address that case, so
core's maintainers might decline it. Asking in the discussion before opening a PR would
settle it, and the tap works regardless.

## Order

1. Make the repo public, tag `v0.1.0`, and publish a GitHub release with the
   `just release v0.1.0` binaries.
2. Create the tap, add the formula, and test it with `--build-from-source`, `test` and
   `audit`.
3. Uncomment the Homebrew row in the README's install table.
4. Automate the bump on release.

# go-whassup

Single-binary terminal WhatsApp client in Go. Keep dependencies minimal and build
with `CGO_ENABLED=0`: one static binary, no extra service or cgo runtime.

## Working in this repo

- Preserve unrelated working-tree changes. Do not commit, push, merge, publish,
  or add a git remote unless asked.
- Use the package that owns the concern and its existing abstractions before
  adding another package. Keep `cmd/go-whassup/main.go` limited to startup/exit;
  `internal/app` wires dependencies, with business logic in other packages.
- Update docs with the behavior they describe. Start at `docs/README.md`; read
  `docs/architecture.md` for boundaries/event flow, `docs/pairing.md` for pairing,
  `docs/storage.md` for persistence, and `docs/config.md` before adding settings.
  New packages or protocol findings also need architecture/index updates.
- Keep this file focused on durable project rules. Put detailed design and
  troubleshooting in `docs/`, and keep executable checks in the Justfile/CI.
- Finish with the result, validation, and a concrete recommended next step from
  remaining work or `docs/roadmap.md`. Distinguish work completed from anything
  needing the user's account, manual testing, or an explicit scope decision.

## Toolchain and validation

`mise.toml` owns pinned tool versions and sets `GOTOOLCHAIN=local` and
`CGO_ENABLED=0`. Use `just` (recipes invoke mise) or `mise exec -- go …`;
a bare `go` may use the wrong version. `go.mod`'s `go` directive is the minimum
language version, not the pinned toolchain. Change pins only for a requested
maintenance/toolchain task; preserve the environment settings and only raise the
minimum language version when required, explaining why.

| Command | Purpose |
| --- | --- |
| `just check` | Required completion gate: formatting, build, vet, lint, tests |
| `just test [flags]` | Focused tests, e.g. `just test -run TestX -v` |
| `just build` / `just run [args]` | Build / run; TUI needs a real terminal; CLI usage in `docs/usage.md` |
| `just fmt` / `just tidy` | Format Go / tidy modules |
| `just lint [flags]` | Enforce `.golangci.yml`; fix violations instead of weakening checks |
| `just outdated` | Inspect module updates without changing the module files (network) |
| `just vulncheck` | Scan reachable code for known Go vulnerabilities (network) |
| `just index` | Rebuild the Trail code map |
| `just emoji` / `just gif` | Regenerate emoji data / record fictional demo assets |
| `just release vX.Y.Z` | Build cgo-free Darwin/Linux release artifacts locally |

Run `just check` before declaring a change done. Tests use disposable fixtures;
run them and fix failures caused by the change without asking at each step.
After dependency changes, also run `just tidy` and `just vulncheck`. Report
checks that could not run and why. Add a recipe for recurring commands.

## Dependencies and APIs

- Stdlib first. Explain each new module in the change summary (and commit message
  if a commit is requested). Test-only modules are acceptable when they replace
  meaningful harness code.
- TUI uses `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, and
  `charm.land/bubbles/v2`. Do not mix in v1: v2 `View()` returns `tea.View`, keys
  are `tea.KeyPressMsg`, and alt-screen belongs on the View.
- Only `internal/wa` imports `go.mau.fi/whatsmeow`. UI code uses `wa.Client` and
  tests share `internal/wa/watest`; do not modify upstream protobuf schemas.
- SQLite is `modernc.org/sqlite` (pure Go), opened only in `internal/sqlite`.
  Never introduce `mattn/go-sqlite3` or another cgo requirement.
- `internal/voice` owns audio devices and the Opus codec; `internal/wa` owns native
  WhatsApp voice-note sending. Read `docs/voice.md` before changing either flow.
- Config is a typed `internal/config` struct with TOML via
  `github.com/pelletier/go-toml/v2`; follow `docs/config.md`, not viper.
- Verify dependency APIs against installed versions with gopls `go_package_api`
  or `mise exec -- go doc <pkg>.<Symbol>` before using them.
- Use gopls `go_symbol_references` before signature changes and
  `go_rename_symbol` for renames. Run `go_diagnostics` on edited Go files when
  the MCP tools are available; otherwise use compiler/vet/lint diagnostics.
- `docs/dependencies.md` describes update ownership: the weekly workflow owns
  whatsmeow; Renovate owns other modules, Actions, and mise pins. Review updates
  and their CI results; automation does not authorize merging or major migrations.

## Runtime invariants

- **TEA:** mutate UI state only in `Update()`. Async work returns a `tea.Msg`
  through a `tea.Cmd`. Receive whatsmeow events through `wa.Subscribe`; a command
  waits on `sub.C` and returns the event. Do not use `p.Send` or touch/render UI
  state from background goroutines.
- **Persistence:** subscriptions can drop events when full. Message recording
  needs its own non-lossy handler. whatsmeow persists sessions, not our messages;
  `internal/store` owns `messages.db`. Message text/media keys go through Codec.
- **Terminal:** Bubble Tea owns stdout/stderr while running. Send all logs,
  including whatsmeow logs, through `slog` to
  `$XDG_STATE_HOME/go-whassup/wa.log`; surface user-facing errors in the View.
  Headless commands use their existing `Console` abstraction.
- **Errors:** wrap with context and return, or surface in the UI. Do not discard
  errors with `_ =`; intentionally ignored errors require an explanatory comment.
- **Privacy:** runtime data belongs in XDG locations via `internal/paths`, never
  the repo. Data directories are `0700`; DBs and cached media are `0600`. Do not
  log or commit session credentials, encryption keys, or private message content.
- **WhatsApp:** preserve the pairing methods on `wa.Client` and follow
  `docs/pairing.md`. Avoid bot-like behavior: presence spam, automatic read
  receipts, bulk history fetches, or rapid-fire sends.

## Tests

Tests must not require a real WhatsApp account or network access. Drive UI
`Update()` with messages and assert on state or `View().Content`, using
`watest.Client`. Use `t.TempDir()` and `t.Setenv()` for filesystem/environment
fixtures. Keep vulnerability/update queries separate from the offline test suite.

<!-- trail:start -->
## Trail — repo context graph

Use Trail to locate unfamiliar Go code and trace callers before changing shared
behavior: `trail map` for orientation, `trail ask "<question>" --source` for
ranked code spans, `trail grep "<literal>"` for exhaustive indexed matches,
`trail skeleton <file>` for signatures, and `trail callers <symbol>` for edges
(`--direction out` for dependencies, `--depth N` for a wider impact).

Read the full affected definition when a returned span is truncated. For known
files, docs/configs, unindexed code, or unavailable/irrelevant graph results, use
`rg` and read the needed files directly. Refresh with `just index` after large
code changes. Treat graph output as navigation context; source, configuration,
and verified checks establish behavior.
<!-- trail:end -->

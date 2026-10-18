# go-whassup

Single-binary WhatsApp client for the terminal, written in Go. Inspired by waha-tui,
but with a hard goal of **minimal dependencies and no cgo**: one static binary,
nothing else to install or run.

## Toolchain (pinned via mise)

- `mise.toml` is the source of truth for tool versions (Go, just, gopls, golangci-lint) and sets
  `GOTOOLCHAIN=local` and `CGO_ENABLED=0`. Don't change these without being asked.
- `go.mod`'s `go` directive is the *minimum* language version (1.26, required by
  whatsmeow), not the pinned toolchain.
- If mise isn't activated in your shell, a bare `go` may be the wrong version.
  Run tools through `just` (its recipes use `mise exec`) or `mise exec -- go …`.

## Commands (Justfile)

| Command | What it does |
| --- | --- |
| `just check` | fmt-check + build + vet + lint + test. **Run before declaring any change done.** |
| `just build` | Builds `bin/wa-tui` |
| `just run [args]` | Builds and runs: no args = TUI (needs a real terminal); `login [phone]` = pair via phone code; `listen` = headless, prints incoming messages. Flags work before or after the subcommand. Full usage: `docs/usage.md`. |
| `just test [flags]` | `go test ./...` with extra flags, e.g. `just test -run TestX -v` |
| `just lint [flags]` | golangci-lint. `.golangci.yml` enforces import boundaries, bans cgo sqlite and Bubble Tea v1, and forbids printing to the terminal and discarding errors. Fix the code; don't weaken the config. |
| `just fmt` / `just tidy` | gofmt / `go mod tidy` |
| `just logs` | Tails the app log |
| `just index` | Rebuilds the graft context graph |

Add a recipe when a command starts getting run often. Don't leave long ad-hoc
invocations in docs.

## Stack (pinned majors; do not mix)

- TUI: `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2` (and `charm.land/bubbles/v2`
  when needed). **v2 APIs only.** v1 (`github.com/charmbracelet/bubbletea`)
  is incompatible: v2 `View()` returns `tea.View`, keys arrive as `tea.KeyPressMsg`,
  and alt-screen is set on the View, not as a program option.
- Protocol: `go.mau.fi/whatsmeow` (no tagged releases; pseudo-versions).
- Storage: `modernc.org/sqlite` (pure Go). Never `mattn/go-sqlite3`, which needs cgo.
- Config: a typed struct in `internal/config`, stored as TOML via `github.com/pelletier/go-toml/v2`.
  **Not viper** (it's heavy and round-trips badly; see `docs/config.md`). New settings follow
  the "Adding a setting" steps in `docs/config.md`.
- **Verify APIs before using them.** These libraries change often. Check with the
  gopls MCP tool `go_package_api` (or `go doc <pkg>.<Symbol>`) rather than relying on memory.

## Code intelligence (MCP servers in `.mcp.json`)

- **graft** (repo-only context graph): use it to navigate *this repo*: where code
  lives, callers, and blast radius. See the graft section below.
- **gopls** (type-aware, covers dependencies, version pinned in `mise.toml`):
  - `go_package_api`: dependency APIs (whatsmeow, bubbletea). It only sees modules
    already in `go.mod`, so `go get` first, then query.
  - `go_symbol_references`: run before changing a symbol's signature.
  - `go_rename_symbol`: renames.
  - `go_diagnostics`: run on edited files before `just check`.
  - `go_vulncheck`: run after adding or bumping dependencies.

## Dependency policy

- Stdlib first. Each new module needs a one-line justification in the commit message.
- Must build with `CGO_ENABLED=0`.
- Test-only deps (e.g. `charmbracelet/x/exp/teatest`) are acceptable when they replace
  meaningful hand-rolled harness code.

## Docs (`docs/`)

- `docs/` holds design notes and protocol facts that the code alone doesn't explain.
  `docs/README.md` is the index.
- **When you change behavior a doc describes, update that doc in the same commit.**
  New subsystems or non-obvious protocol findings get a doc, or a section in one,
  plus an index entry.
- Read the relevant doc before changing pairing, storage, or event flow.

## Layout

```
cmd/wa-tui/          main.go only: signal ctx -> app.Run -> exit code. Keep it ~20 lines.
internal/app/        composition root: args, paths, logging, wiring. No business logic.
internal/paths/      XDG locations (ConfigFile, DataDir, SessionDB, MessagesDB, LogFile)
internal/config/     typed Config: defaults, Load (strict), Validate, Save (atomic)
internal/logging/    slog file logger
internal/wa/         whatsmeow adapter: Client interface, Event types, Subscribe.
                     ONLY package importing whatsmeow.
internal/wa/watest/  fake wa.Client shared by all tests
internal/headless/   login/listen commands (Console: In/Out/Log)
internal/store/      OUR chat/message schema (step 2). whatsmeow does NOT persist messages.
internal/ui/         Bubble Tea models + Run. Imports internal/wa, never whatsmeow.
```

- **Keep `main.go` short and put logic in `internal/*`.** Add packages as real
  concerns appear, organized by concern.
- **Reuse before adding.** Extend the package that owns a concern, and reuse
  existing abstractions (`wa.Subscribe`, `watest.Client`, `Console`, `paths.Paths`)
  rather than parallel ones. When you add a package, update this block and
  `docs/architecture.md`.

## Invariants

- **TEA:** mutate model state only in `Update()`. Async work returns a `tea.Msg` from a `tea.Cmd`.
- **Goroutines:** whatsmeow event handlers run on background goroutines. Consume events
  via `wa.Subscribe`. In the TUI, a `tea.Cmd` receives the next event from `sub.C` and
  returns it as a `tea.Msg`; don't use `p.Send`. Never render or touch UI state off the
  UI goroutine. Subscriptions drop events when full, so data that must not be lost
  (the message store) needs its own non-lossy handler.
- **Terminal ownership:** Bubble Tea owns stdout/stderr while running. All logging,
  including whatsmeow's logger, goes to `$XDG_STATE_HOME/go-whassup/wa.log` via `slog`.
  No `fmt.Print*` or `log.Print*` to the terminal once the program starts.
- **Errors:** wrap with context (`fmt.Errorf("doing X: %w", err)`) and return, or surface
  in the View. No `_ =` discards. Ignoring an error needs a comment saying why.
- **Runtime data:** session/message DBs live in `$XDG_DATA_HOME/go-whassup/`, created `0700`.
  Never write runtime files into the repo, and never commit them.
- **Pairing:** phone-number code only (`PairPhone`). Keep pairing behind its own method
  on the `wa` interface so QR pairing can be added later without touching event handling.

## Testing

- Tests must never require a real WhatsApp account or network access.
- UI: drive `Update()` with messages and assert on the model or `View().Content`, using a
  fake implementation of the `wa` interface.
- Use `t.TempDir()` and `t.Setenv()` for anything touching paths or env.

## Don't

- Modify whatsmeow protobuf schemas.
- Add behavior that looks like a bot (ban risk): presence spam, automatic read
  receipts, bulk history fetches, rapid-fire sends.
- Commit, push, or add a git remote unless asked. The repo is local-only for now.

<!-- graft:start -->
## Graft — repo context graph

This repo is indexed in `graft/`: small linked markdown nodes that explain each
system and carry exact file:line spans, kept in sync with the code through git.

For ANY task here — understanding how something works, finding where code lives,
or scoping a change — get context from the graph before grepping or opening
source files. Re-ask freely (it's cheap) and reuse literal identifiers you
already have (symbol, error string, file name) as the query. New to this repo?
Run `graft map` first — a token-budgeted orientation (dir clusters, hubs,
hotspots), no LLM, no key.

- Run `graft ask "<your question>" --source` → ranked nodes with the relevant
  code spans inlined (each hit's ≤8-line crux by default; `--full` for whole
  definitions when the crux isn't enough). Match the tool to the task shape:
  for understanding or editing, the top node IS the answer — cite its
  `covers:` file:line spans and edit straight from `--source`. For
  exhaustive tasks ("every occurrence / every caller of this pattern"), ranked
  results are top-N, not complete — run `graft grep "<literal>"` instead
  (exhaustive over indexed files, grouped by enclosing symbol), falling back
  to raw `grep -rn` only for unindexed files.
- `graft skeleton <file>` → every definition's signature + span, ~10× cheaper
  than reading the file; use it to skim an API surface.
- `graft callers <symbol>` gives precomputed, exact edges — who calls this.
  Add `--direction out` for what it calls, or `--depth N` to walk
  transitively for the full blast radius. For structural questions, skip
  ranking and use this directly.
- Or browse: `graft/INDEX.md` lists every node; follow the links.
- Monorepos and folders of multiple repos rank fairly across sub-projects —
  hits carry `[scope/]` labels naming which one they're from. Narrow with
  `graft ask "<task>" --in <scope>/` once you know where you're working.

If a returned span is truncated ("+N more lines"), open the file at that exact
range before finalizing. Only open source files when a node genuinely lacks a
needed detail, and then at the exact file:line the node points to — never
re-read whole files.

After big code changes, refresh the graph with `graft build` (deterministic,
no API key, $0).
<!-- graft:end -->

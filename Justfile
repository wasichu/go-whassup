# Run everything through mise so the pinned toolchain and env (GOTOOLCHAIN, CGO_ENABLED)
# apply even when mise isn't activated in the calling shell.
set positional-arguments

go := "mise exec -- go"
bin := "bin/go-whassup"

# List recipes
default:
    @just --list

# Build the binary into bin/
build:
    {{go}} build -o {{bin}} ./cmd/go-whassup

# Build and run the TUI
run *args: build
    ./{{bin}} "$@"

# Run tests (pass extra flags, e.g. `just test -run TestFoo -v`)
test *args:
    {{go}} test ./... {{args}}

vet:
    {{go}} vet ./...

# Lint (config in .golangci.yml enforces the AGENTS.md invariants)
lint *args:
    mise exec -- golangci-lint run ./... {{args}}

# Format all Go code
fmt:
    {{go}} fmt ./...

# Fail if any file needs formatting
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out="$(mise exec -- gofmt -l .)"
    if [ -n "$out" ]; then echo "needs gofmt:"; echo "$out"; exit 1; fi

tidy:
    {{go}} mod tidy

# Everything that must pass before a commit
check: fmt-check build vet lint test

# Follow the app log (the TUI owns the terminal, so logs go here)
logs:
    tail -F "${XDG_STATE_HOME:-$HOME/.local/state}/go-whassup/wa.log"

clean:
    rm -rf bin

# Rebuild the graft context graph (deterministic, no API key)
index:
    graft build

# Cross-compile cgo-free release binaries into dist/, stamped with the
# version (e.g. `just release v0.1.0`), plus SHA256SUMS.
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    case "{{version}}" in v[0-9]*) ;; *) echo "version must look like v0.1.0"; exit 1;; esac
    rm -rf dist && mkdir -p dist
    for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
      os="${target%/*}"; arch="${target#*/}"; ext=""
      if [ "$os" = windows ]; then ext=".exe"; fi
      GOOS="$os" GOARCH="$arch" mise exec -- go build -trimpath \
        -ldflags "-s -w -X github.com/wasichu/go-whassup/internal/app.Version={{version}}" \
        -o "dist/go-whassup-{{version}}-$os-$arch$ext" ./cmd/go-whassup
    done
    cd dist && if command -v sha256sum >/dev/null; then sha256sum go-whassup-* > SHA256SUMS; else shasum -a 256 go-whassup-* > SHA256SUMS; fi
    ls -l

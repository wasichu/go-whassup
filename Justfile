# Run everything through mise so the pinned toolchain and env (GOTOOLCHAIN, CGO_ENABLED)
# apply even when mise isn't activated in the calling shell.
go := "mise exec -- go"
bin := "bin/wa-tui"

# List recipes
default:
    @just --list

# Build the binary into bin/
build:
    {{go}} build -o {{bin}} ./cmd/wa-tui

# Build and run the TUI
run *args: build
    ./{{bin}} {{args}}

# Run tests (pass extra flags, e.g. `just test -run TestFoo -v`)
test *args:
    {{go}} test ./... {{args}}

vet:
    {{go}} vet ./...

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
check: fmt-check build vet test

# Follow the app log (the TUI owns the terminal, so logs go here)
logs:
    tail -F "${XDG_STATE_HOME:-$HOME/.local/state}/go-whassup/wa.log"

clean:
    rm -rf bin

# Rebuild the graft context graph (deterministic, no API key)
index:
    graft build

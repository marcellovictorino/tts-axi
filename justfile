set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default:
    @just --list

# Compile every package.
build:
    go build ./...

# Build the tts-axi binary into ./tts-axi.
bin:
    go build -o tts-axi .

# Run the tests: no model, no network, no Neural Engine (ffmpeg needed for the command tests).
test:
    go test ./...

# Vet and lint (brew install golangci-lint).
lint:
    go vet ./...
    golangci-lint run ./...

# Everything CI runs on every change: lint, then tests.
check: lint test

# Download Gradium's pinned runtime (39 MB) and build the Swift helper in a temp folder, as CI does.
helper-test:
    TTS_AXI_BUILD_HELPER=1 go test -run TestHelperBuildsAgainstThePinnedRuntime -v ./internal/engine

# Install tts-axi from this checkout into Go's bin folder (usually ~/go/bin).
install:
    go install .
    @echo "installed: $(command -v tts-axi || echo "$(go env GOPATH)/bin/tts-axi (add it to PATH)")"

# Download the runtime and English model into ~/.tts-axi, build the helper, prepare the default speed. Loads no model.
setup:
    tts-axi doctor --fix

# Install the agent skill for Claude Code, Codex and Pi.
skill:
    tts-axi skill install

# Check the installation.
doctor:
    tts-axi doctor

# Speak one short line for real into a throwaway library and check the result. Uses the installed tts-axi.
smoke:
    #!/usr/bin/env bash
    set -euo pipefail
    lib="$(mktemp -d)/tts-axi-smoke"
    echo "memory before: $(sysctl -n kern.memorystatus_vm_pressure_level) (1 normal, 2 warning, 4 critical); swap $(sysctl -n vm.swapusage | awk '{print $6}')"
    out="$(TTS_AXI_LIBRARY="$lib" tts-axi say "Good morning. This is tts-axi, speaking on this Mac." --format toon)"
    echo "$out"
    grep -Eq '^engine: (ane|cpu)$' <<<"$out" || { echo "smoke: no engine in the result"; exit 1; }
    audio="$(sed -n 's/^path: //p' <<<"$out")"
    ffprobe -v error -show_entries format=duration -of default=nw=1 "$audio"
    if pgrep -fl tts-axi-helper; then echo "smoke: a helper is still running"; exit 1; fi
    echo "memory after: $(sysctl -n kern.memorystatus_vm_pressure_level); swap $(sysctl -n vm.swapusage | awk '{print $6}')"
    echo "smoke ok; listen with: afplay '$audio'"

# Full pre-merge validation: checks, install from this checkout, setup, a real spoken line.
validate: check install setup doctor smoke

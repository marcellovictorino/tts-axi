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

# Speak a sample sentence in every catalog voice of a language (en, pt or fr) into one library episode, a chapter per voice.
samples lang="en":
    #!/usr/bin/env bash
    set -euo pipefail
    case "{{lang}}" in
      en) line="Hello, I'm NAME. Here is your briefing for today: three meetings, two invoices, and a little rain this afternoon." ;;
      pt) line="Olá, eu sou NAME. Aqui está o seu resumo de hoje: três reuniões, duas faturas e um pouco de chuva à tarde." ;;
      fr) line="Bonjour, je suis NAME. Voici votre programme du jour : trois réunions, deux factures et un peu de pluie cet après-midi." ;;
      *) echo "samples: lang must be en, pt or fr"; exit 2 ;;
    esac
    script="$(mktemp -d)/voice-samples-{{lang}}.md"
    printf -- '---\ntitle: Voice samples ({{lang}})\nlang: {{lang}}\n---\n' > "$script"
    # Each voice's name is the first field of its row in the TOON table.
    tts-axi voices --lang "{{lang}}" --format toon | sed -n 's/^  //p' | while IFS= read -r row; do
      name="${row%%,*}"; name="${name//\"/}"
      printf '## %s\n%s: %s\n\n' "$name" "$name" "${line//NAME/$name}" >> "$script"
    done
    tts-axi render "$script"

# Fail when program files changed since base without a CHANGELOG.md entry (CI runs this on every pull request).
changelog-check base="origin/main":
    scripts/changelog-check {{base}}

# Prepare a release on a new branch: move Unreleased under the version, bump the version constant, commit. Then open a pull request.
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    v="{{version}}"; v="${v#v}"
    [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "release: give a version such as 0.2.0"; exit 2; }
    [[ -z "$(git status --porcelain)" ]] || { echo "release: commit or stash your changes first"; exit 1; }
    git fetch -q origin
    # Check main's changelog before creating anything, so a refusal leaves no branch behind.
    log="$(git show origin/main:CHANGELOG.md)"
    grep -q "^## \[$v\]" <<<"$log" && { echo "release: CHANGELOG.md already has $v"; exit 1; }
    # The lines between ## [Unreleased] and the next section must not be empty.
    awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{f=0} f&&NF{n++} END{exit n?0:1}' <<<"$log" || { echo "release: nothing under ## [Unreleased] in CHANGELOG.md on main"; exit 1; }
    git switch -q -c "release/v$v" origin/main
    awk -v v="$v" -v d="$(date +%Y-%m-%d)" '{print} /^## \[Unreleased\]/{print ""; print "## [" v "] - " d}' CHANGELOG.md > CHANGELOG.md.new && mv CHANGELOG.md.new CHANGELOG.md
    sed -i '' -E "s/^const Version = \"[^\"]+\"/const Version = \"$v\"/" internal/version/version.go
    grep -q "const Version = \"$v\"" internal/version/version.go || { echo "release: could not set the version constant"; exit 1; }
    git commit -q -am "chore: release v$v"
    echo "release: branch release/v$v is ready. Push it and open a pull request:"
    echo "  git push -u origin release/v$v && gh pr create --fill"
    echo "After it merges: just tag $v"

# Tag a merged release on main and push the tag.
tag version:
    #!/usr/bin/env bash
    set -euo pipefail
    v="{{version}}"; v="${v#v}"
    git fetch -q origin
    got="$(git show origin/main:internal/version/version.go | sed -n -E 's/^const Version = "([^"]+)"/\1/p')"
    [[ "$got" == "$v" ]] || { echo "tag: main is at version $got, not $v; merge the release pull request first"; exit 1; }
    git show origin/main:CHANGELOG.md | grep -q "^## \[$v\]" || { echo "tag: CHANGELOG.md on main has no $v section"; exit 1; }
    git tag -a "v$v" -m "tts-axi v$v" origin/main
    git push origin "v$v"
    echo "tagged v$v; install it with: go install github.com/marcellovictorino/tts-axi@v$v"

# Full pre-merge validation: checks, install from this checkout, setup, a real spoken line.
validate: check install setup doctor smoke

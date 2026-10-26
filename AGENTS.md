# AGENTS.md

tts-axi is a Go CLI (module `github.com/marcellovictorino/tts-axi`) that turns text, scripts and slide notes into audio with Phonon: English through a Swift helper on Gradium's Core ML runtime (Neural Engine, CPU fallback), Portuguese and French through ptts on the CPU, with the multilingual weights downloaded from Ollama's registry (no Hugging Face account or token; Ollama itself is not used). Apple silicon macOS only.

## Commands

```bash
go build ./...                 # compile
go test ./...                  # unit and command tests (no model, no network; ffmpeg needed for cmd tests)
go vet ./...
golangci-lint run ./...        # config in .golangci.yml (brew install golangci-lint)
TTS_AXI_BUILD_HELPER=1 go test -run TestHelperBuilds ./internal/engine   # download the runtime and build the Swift helper
```

Run `just check` (vet, golangci-lint, `go test ./...`) before every commit. `just` lists the other recipes: `install`, `setup`, `smoke` (a real spoken line into a throwaway library) and `validate` (all of them).

## Rules

- **Changelog:** every pull request that changes the program adds a line under `## [Unreleased]` in `CHANGELOG.md` (Keep a Changelog). CI's `changelog` job (`scripts/changelog-check`, also `just changelog-check`) fails without one; tests, docs and CI files alone need none, and the `no-changelog` label skips it for changes nobody using tts-axi would notice.
- **Releases:** never bump the version in an ordinary pull request. `just release 0.2.0` makes a `release/v0.2.0` branch that moves Unreleased under the new version and bumps `internal/version`; after that pull request merges, `just tag 0.2.0` tags main and pushes the tag.
- **Tests never touch the real home.** Use `t.TempDir()` with `HOME`, `TTS_AXI_HOME`, `TTS_AXI_LIBRARY` and `XDG_CONFIG_HOME` (see `newEnv` in `cmd/cmd_test.go`). The Swift helper and `uv` are faked by `internal/testutil` (test-binary re-exec), and `testutil.Registry` serves a real subset of Ollama's model; no test loads a model or reaches the network.
- **Memory is a hard constraint.** Every helper run goes through `guard.Run`; never start a helper without it, never hold a model between renders, and keep the stops (critical pressure, any pressure rise, swap growth over 256 MiB a minute, one CPU retry, then fail without writing).
- **Pinned downloads.** Release assets are pinned by size and SHA-256 in `internal/engine/assets.go`; a new runtime version updates all three together. The multilingual model's Ollama manifest is pinned the same way, and its digests pin every blob; a new Ollama tag updates the manifest pin and the README's "Portuguese and French" section.
- **No Hugging Face sign-in.** No route may need a Hugging Face account or token: the multilingual weights come from Ollama's registry (an undocumented address) to keep setup to one `doctor --fix`.
- **Self-contained:** do not link to or depend on outside local projects or paths.
- **Idiom:** add a short comment where a Go feature is non-obvious (embedding, build tags, process groups, test-binary re-exec).

## Agent-facing output (axi)

When stdout is not a terminal, output is TOON on stdout: minimal fields, `help[n]:` next steps, definitive empty states (`episodes[0]:`), errors as `error:` plus `help[n]:` on stdout. Exit codes: 0 ok, 1 failure, 2 usage error (unknown flag or command, with the valid choices listed). No prompts, ever. `--format toon|human` overrides detection; `NO_COLOR` is respected.

## Layout

```
main.go                 calls cmd.Execute()
skills/                 the agent skill (SKILL.md), embedded in the binary
cmd/                    one cobra command per file; produce.go is the shared speak-join-publish pipeline
internal/config         paths (~/.tts-axi, ~/Music/tts-axi), config.toml, the render lock
internal/voices         embedded voice catalog, default order, speaker casting
internal/script         text, script and deck parsing into segments and pauses
internal/engine         helpers (embedded Swift and Python sources), engine policy, downloads, Ollama registry merge, speed bundles
internal/guard          memory pressure and swap watcher, process-group kill
internal/audio          WAV reading, silence, trimming, ffmpeg joins with chapters
internal/library        episode folders, index.html, feed.xml, the deck player
internal/output         TOON encoder, human rendering, errors
internal/version        version constant
internal/testutil       fake helpers, fake sampler, real tensor fixtures
```

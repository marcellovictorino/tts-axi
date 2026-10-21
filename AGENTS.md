# AGENTS.md

tts-axi is a Go CLI (module `github.com/marcellovictorino/tts-axi`) that turns text, scripts and slide notes into audio with Phonon: English through a Swift helper on Gradium's Core ML runtime (Neural Engine, CPU fallback), Portuguese and French through ptts on the CPU. Apple silicon macOS only.

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

- **Changelog:** every user-visible change adds a line under `## [Unreleased]` in `CHANGELOG.md` (Keep a Changelog). The version constant in `internal/version` and the top released section move together.
- **Tests never touch the real home.** Use `t.TempDir()` with `HOME`, `TTS_AXI_HOME`, `TTS_AXI_LIBRARY` and `XDG_CONFIG_HOME` (see `newEnv` in `cmd/cmd_test.go`). The Swift helper and `uv` are faked by `internal/testutil` (test-binary re-exec); no test loads a model.
- **Memory is a hard constraint.** Every helper run goes through `guard.Run`; never start a helper without it, never hold a model between renders, and keep the stops (critical pressure, any pressure rise, swap growth over 256 MiB a minute, one CPU retry, then fail without writing).
- **Pinned downloads.** Release assets are pinned by size and SHA-256 in `internal/engine/assets.go`; a new runtime version updates all three together.
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
internal/engine         helpers (embedded Swift and Python sources), engine policy, downloads, speed bundles
internal/guard          memory pressure and swap watcher, process-group kill
internal/audio          WAV reading, silence, trimming, ffmpeg joins with chapters
internal/library        episode folders, index.html, feed.xml, the deck player
internal/output         TOON encoder, human rendering, errors
internal/version        version constant
internal/testutil       fake helpers, fake sampler, real tensor fixtures
```

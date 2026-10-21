# AGENTS.md

tts-axi is a Go CLI (module `github.com/marcellovictorino/tts-axi`) that turns text, scripts and slide notes into audio with Phonon. Apple silicon macOS only.

## Commands

```bash
go build ./...
go test ./...
go vet ./...
golangci-lint run ./...        # config in .golangci.yml (brew install golangci-lint)
```

Run `go test ./...` and `golangci-lint run ./...` before every commit.

## Rules

- **Changelog:** every user-visible change adds a line under `## [Unreleased]` in `CHANGELOG.md` (Keep a Changelog).
- **Tests never touch the real home.** Use `t.TempDir()` for every path the CLI writes.
- **Self-contained:** do not link to or depend on outside local projects or paths.

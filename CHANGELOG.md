# Changelog

All notable changes to tts-axi are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) (0.x while the interface settles).

## [Unreleased]

## [0.1.0] - 2026-10-21

### Added

- `tts-axi say` speaks text, a file or standard input in one voice into one audio file (mp3, m4a or wav), split by paragraph, with Markdown marks dropped.
- `tts-axi render` speaks a multi-voice script with `## chapter` headings, `Speaker: line` turns and `[pause 2s]` marks into one file with chapter marks and real silence.
- `tts-axi deck` narrates Markdown slides from their speaker notes: one clip per slide, `deck.mp3` with a chapter per slide, `timings.json` and an auto-advancing `player.html`; `--images` shows exported slide pictures.
- English speaks with Phonon 31M through Gradium's Core ML runtime (xn-ptts v0.4.0) on the Neural Engine, built into a small Swift helper by `tts-axi doctor --fix`. It falls back to the same bundle on the CPU when the Neural Engine fails or memory pressure is above normal at start; `--engine cpu` forces the CPU.
- Portuguese and French speak with phonon-40m-multilingual through ptts 0.4.1 on the CPU, run by uv.
- A memory guard stops and unloads a helper on critical pressure, any pressure rise or swap growth over 256 MiB a minute, waits for pressure to settle, retries once on the CPU, and otherwise fails without writing anything. One render runs at a time.
- Default voices: Hugh then Bryony (English), Davi then Kauã, Manuela-Lu, Débora (Portuguese), Augustin then Coralie (French); default speed duration_delta 0.15, also on the Core ML route, whose bundle is prepared per speed.
- The library at `~/Music/tts-axi`: one folder per episode with its audio, transcript and `episode.json`, plus a regenerated `index.html` and `feed.xml`. `tts-axi library` lists, `library rm` deletes, `library rebuild` refreshes.
- A justfile: `just check`, `just install`, `just setup`, `just smoke` (one real spoken line) and `just validate`.
- `tts-axi voices`, `tts-axi doctor [--fix]`, `tts-axi skill install` (Claude Code, Codex and Pi) and `tts-axi version`.

# Changelog

All notable changes to tts-axi are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) (0.x while the interface settles).

## [Unreleased]

### Added

- Cloned voices. `tts-axi voice add <name> --clip <audio> --text "<words>"` (or `--text-file`) converts a recording to a 24 kHz mono WAV and keeps it with its words in `~/.tts-axi/voices/<name>`, outside the repository and the library. `--voice <name>` then works in `say`, `deck` and `render` (a bare `--voice` in `render` is the narrator's voice), for English, spoken by Qwen3-TTS 1.7B Base (8-bit) through mlx-audio, run by uv, under the memory guard. `doctor --fix` downloads the 3 GB model from Hugging Face with no account, pinned by size and SHA-256 per file; a cloned render downloads it on first use if it is missing.
- `tts-axi voices` lists cloned voices and has a `model` column (`phonon` or `qwen`); results, `library` and `episode.json` name the model that spoke. Built-in voices stay on Phonon, the default.
- The memory guard treats a cloned voice's first load leniently: until the first line is spoken only critical pressure lasting 5 seconds stops the run, then swap growth is measured from that moment, pressure at warning is tolerated and critical pressure stops it at once. Built-in voices keep the existing stops.

### Changed

- Portuguese and French no longer need a Hugging Face account or token. `tts-axi doctor --fix` downloads the same phonon-40m-multilingual weights from Ollama's registry (`phonon:40m-multilingual`, 122 MB, pinned by SHA-256) with no sign-in, and merges them into `~/.tts-axi/models/phonon-40m-multilingual`, which ptts loads. Ollama itself is not needed. Run `tts-axi doctor --fix` once after upgrading; the old copy in the Hugging Face cache (`~/.cache/huggingface/hub/models--gradium--phonon-40m-multilingual`) is no longer used and can be deleted.

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
- The multilingual model is pinned to one revision and downloaded once by `tts-axi doctor --fix` with a Hugging Face token; renders then read it from the cache with no token. The README's "Portuguese and French" section walks through the account, the token, and keeping it in 1Password or the Keychain.
- Shell completion for voices, languages, engines, formats and episode ids: `source <(tts-axi completion zsh)`.
- `just samples <lang>` speaks one sentence in every catalog voice of en, pt or fr into one library episode, a chapter per voice.
- Releases: `just release <version>` prepares a release pull request and `just tag <version>` tags it after the merge; CI fails a pull request that changes the program without a changelog line.
- A justfile: `just check`, `just install`, `just setup`, `just smoke` (one real spoken line) and `just validate`.
- `tts-axi voices`, `tts-axi doctor [--fix]`, `tts-axi skill install` (Claude Code, Codex and Pi) and `tts-axi version`.

# tts-axi

Local text to speech for any agent. `tts-axi` turns text, multi-voice scripts and slide speaker notes into audio with [Phonon](https://github.com/gradium-ai/xn-ptts), on the Neural Engine of an Apple silicon Mac.

> Issues are welcome. Pull requests are not accepted; please open an issue instead.

- **say**: text, a file or standard input, in one voice, into one audio file.
- **render**: a script with several voices, `## chapters` and `[pause 2s]` marks, into one mp3 or m4a with chapter marks.
- **deck**: one clip per slide from the speaker notes, plus a player page that turns the slides as the narration plays.

Everything lands in one library folder, `~/Music/tts-axi`, with an `index.html` page that plays every episode and a `feed.xml` podcast feed. Delete an episode by deleting its folder.

Like [pill](https://github.com/marcellovictorino/pill), it follows the axi conventions for agent-facing tools: compact [TOON](https://github.com/toon-format/toon) output when piped, tables on a terminal, `help[n]:` next steps, exit codes `0`/`1`/`2`, and a skill for Claude Code, Codex and Pi.

Apple silicon, macOS 15 or later.

## Requirements

- Go, to build: `brew install go`
- ffmpeg, to join and encode audio: `brew install ffmpeg`
- Xcode Command Line Tools, for `swiftc`, which builds the speech helper: `xcode-select --install`
- uv, for Portuguese and French: `brew install uv`

## Install

```bash
go install github.com/marcellovictorino/tts-axi@latest
tts-axi doctor --fix      # downloads the runtime (39 MB) and English model (137 MB), builds the helper
tts-axi skill install     # teaches Claude Code, Codex and Pi to use it
```

`doctor --fix` checks every download against a pinned SHA-256, and loads no model. Running it again is a no-op.

That is all English needs: no account and no token. Only Portuguese and French need the one-time Hugging Face step in [Portuguese and French](#portuguese-and-french).

From a checkout, [just](https://github.com/casey/just) (`brew install just`) runs the same steps:

```bash
just install    # go install . into ~/go/bin
just setup      # tts-axi doctor --fix
just skill      # tts-axi skill install
just smoke      # speak one line for real into a throwaway library and check it
just validate   # lint, tests, install, setup, doctor and smoke, in that order
```

## Quick start

```bash
tts-axi say "Good morning. Here is your briefing for today."
tts-axi say --file notes.md --voice Bryony
echo "Bom dia a todos." | tts-axi say - --lang pt
tts-axi render day.md
tts-axi deck talk.md
tts-axi library            # list episodes; open ~/Music/tts-axi/index.html to play them
```

A script for `render`:

```markdown
---
title: Your day ahead
voices: {Host: Hugh, Guest: Bryony}
---
## Calendar
Host: Morning. Three things today.
[pause 1s]
Guest: First, the ten o'clock review.

## Weather
Host: A little rain this afternoon.
```

A deck for `deck` is Markdown slides separated by `---` (Marp or Slidev), with each slide's narration in an HTML comment, `<!-- like this -->`. Pass `--images` with exported slide pictures to show them in the player instead of the simple built-in rendering.

## Voices and speed

| Language | Voices, in order | Engine |
|---|---|---|
| English | Hugh, Bryony | Phonon 31M on the Neural Engine, CPU fallback |
| Portuguese (Brazil) | Davi, Kauã, Manuela-Lu, Débora | Phonon 40M multilingual on the CPU |
| French | Augustin, Coralie | Phonon 40M multilingual on the CPU |

The first voice speaks by default; script speakers take the others in order. `tts-axi voices` lists all 64 catalog voices, and `just samples pt` (or `en`, `fr`) speaks one sentence in every voice of a language into a library episode with a chapter per voice, so you can pick by ear. The samples are made on your Mac; the repository keeps no audio. The default speed is Phonon's `duration_delta` at `0.15`, a little slower than the model's own pace; `--speed` changes it.

## Portuguese and French

English needs none of this section. Portuguese and French use the gated [phonon-40m-multilingual](https://huggingface.co/gradium/phonon-40m-multilingual) model (CC BY-NC 4.0, non-commercial). It needs a Hugging Face token **once**, to download it. tts-axi pins the model to one revision, so after that every render reads it from the local cache and never asks for the token again: no sign-in prompt, no fingerprint per sound.

1. Create a free account at [huggingface.co/join](https://huggingface.co/join).
2. Open [the model page](https://huggingface.co/gradium/phonon-40m-multilingual) and accept its terms.
3. Create a token at [huggingface.co/settings/tokens](https://huggingface.co/settings/tokens): type **Read** is enough. A fine-grained token needs "Read access to contents of all public gated repos you can access".
4. Give the token to one `tts-axi doctor --fix` run, in whichever way suits you:

   ```bash
   # 1Password: one Touch ID, once
   HF_TOKEN="$(op read 'op://<vault>/<item>/<field>')" tts-axi doctor --fix

   # macOS Keychain: store it once (you are prompted for it, so it stays out of your shell history) ...
   security add-generic-password -a "$USER" -s huggingface-token -w
   # ... then use it once
   HF_TOKEN="$(security find-generic-password -a "$USER" -s huggingface-token -w)" tts-axi doctor --fix

   # Or Hugging Face's own sign-in, which saves the token in ~/.cache/huggingface/token
   uvx --from huggingface_hub hf auth login
   tts-axi doctor --fix
   ```

5. `tts-axi doctor` now shows `multilingual model: ok`. Try `tts-axi say "Bom dia" --lang pt`.

The multilingual model itself speaks five languages: English, French, German, Portuguese (Brazil) and Spanish, and Phonon's catalog has 13 German and 25 Spanish voices. tts-axi uses it for Portuguese and French only for now, and keeps English on the smaller Neural Engine model; German and Spanish need only catalog and default-voice entries to be added.

The token is only read by Hugging Face's own download tool; tts-axi never stores or prints it. With the model downloaded you can remove a saved token (`uvx --from huggingface_hub hf auth logout`) and French and Portuguese keep working.

## Memory

A render loads a model only while it speaks, then exits. English on the Neural Engine peaks around 100 MB. One render runs at a time; others wait for a file lock.

While a helper speaks, tts-axi watches memory pressure and swap, and stops and unloads the model at once on:

- critical memory pressure;
- any rise in pressure above where the render started;
- swap growing by more than 256 MiB within a minute.

It then waits for pressure to settle and retries once on the CPU. If that is stopped too, the render fails and writes nothing. English starts on the CPU when pressure is already above normal, and falls back to the CPU if the Neural Engine fails. Every result names the engine that spoke it (`ane`, `cpu` or `ptts-cpu`); `--engine cpu` forces the CPU.

## On your phone

The library is a folder of static files. To play it on your phone privately over [Tailscale](https://tailscale.com):

```bash
tailscale serve --bg ~/Music/tts-axi
```

Open the address it prints on the phone. To give `feed.xml` full links, put that address in `~/.config/tts-axi/config.toml` as `base_url`. Podcast apps that fetch feeds from their own servers cannot reach a private address; the web page works everywhere.

## Shell completion

Commands, flags, voice names, languages and episode ids complete in the shell. For zsh, one line:

```bash
echo 'source <(tts-axi completion zsh)' >> ~/.zshrc
```

Open a new terminal afterwards. Your `~/.zshrc` must load completion first (`autoload -U compinit && compinit`), which most setups, Oh My Zsh included, already do. `tts-axi completion --help` covers bash and fish.

## Configuration

Everything is optional. `~/.config/tts-axi/config.toml`:

```toml
speed = 0.15                     # Phonon duration_delta
base_url = "https://my-mac.example.ts.net"
library = "~/Music/tts-axi"

[voices]
en = ["Hugh", "Bryony"]          # default voice order per language
```

Environment overrides: `TTS_AXI_HOME` (runtime, helper and models, default `~/.tts-axi`), `TTS_AXI_LIBRARY`, `TTS_AXI_BASE_URL`.

## How it works

```
tts-axi (Go)                        flags, scripts, voices, lock, memory guard,
  │                                  ffmpeg joins, library page and feed
  ├── tts-axi-helper (Swift)        English: Phonon 31M through Gradium's Core ML
  │                                  runtime, Neural Engine or CPU
  └── uv run ptts (Python)          Portuguese, French: Phonon 40M multilingual, CPU
```

Each helper reads a batch of lines as JSON, loads the model once, writes one WAV per line and exits. tts-axi trims each clip, inserts the gaps and pauses as real silence, and joins everything with ffmpeg, writing chapter marks into the file.

Phonon's speed is a conditioning input. The Python runtime takes it per call; the released Core ML bundle bakes it in at `0`. tts-axi prepares a bundle per speed by shifting that baked-in vector with the speed conditioner's weights, which it reads (128 KiB) from the original checkpoint on Hugging Face.

## Development

See [AGENTS.md](AGENTS.md). `just check` runs what CI runs (vet, lint, tests); `just` lists every recipe. Every pull request that changes the program adds a line to [CHANGELOG.md](CHANGELOG.md), and CI checks it. Releases are `just release <version>` (a pull request that bumps the version), then `just tag <version>` once it merges. The tests need no model, network or Neural Engine: the helpers are faked.

## Licence

MIT. Phonon's models and runtime are Gradium's, under their own licences.

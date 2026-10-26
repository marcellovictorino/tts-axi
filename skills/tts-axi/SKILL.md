---
name: tts-axi
description: Turn text, a multi-voice script or a slide deck's speaker notes into audio on this Mac with the tts-axi CLI (Phonon, local, English, Portuguese and French). Use when asked to read something aloud, narrate a deck, make a podcast or briefing episode, or produce an audio file from text.
---

# tts-axi

`tts-axi` speaks text locally with Phonon: English on the Neural Engine (CPU
fallback), Portuguese and French on the CPU. Results go into a library folder
(`~/Music/tts-axi`) with an `index.html` player page and a `feed.xml`, unless
you pass `--out`. Every command prints compact
[TOON](https://github.com/toon-format/toon) when piped, ending with `help[n]:`
next steps. Exit codes: `0` ok, `1` failure, `2` usage error (valid choices
listed). Nothing ever prompts.

## Common tasks

| Goal | Command |
|---|---|
| Read a short text aloud | `tts-axi say "text"` |
| Read a file or piped text | `tts-axi say --file notes.md` · `echo text \| tts-axi say -` |
| Write to a specific file, not the library | `tts-axi say "text" --out /path/x.mp3` (`.mp3`, `.m4a`, `.wav`) |
| Several voices, chapters, pauses | `tts-axi render script.md` |
| Narrate slides from speaker notes | `tts-axi deck slides.md [--images pngs/]` |
| Pick a voice | `tts-axi voices [--lang en\|pt\|fr]` |
| Find or delete earlier audio | `tts-axi library` · `tts-axi library rm <episode>` |
| Something is wrong | `tts-axi doctor` (`tts-axi doctor --fix` installs the runtime) |

Defaults: English voice Hugh (then Bryony), Portuguese Davi (then Kauã,
Manuela-Lu, Débora), French Augustin (then Coralie), speed `0.15` (a little
slower than the model's pace). `--voice`, `--lang`, `--speed` and `--title`
override them.

## Script format (render)

```markdown
---
title: Your day ahead
lang: en
voices: {Host: Hugh, Guest: Bryony}
---
## Calendar
Host: Morning. Three things today.
[pause 1s]
Guest: First, the ten o'clock review.

## News
Davi: Bom dia. Uma notícia do Brasil.
```

- `## Heading` starts a chapter (written as chapter marks into the file).
- `Name: words` starts a turn; following lines continue it until a blank line.
- A speaker named after a catalog voice (`Hugh:`, `Davi:`) uses that voice;
  other names take the language's default voices in order, or map them in
  `voices:` or with `--voice Name=Voice`.
- `[pause 1.5s]` or `[pause 500ms]` inserts real silence. Phonon has no other
  markup: no SSML, no emotion tags. Write emotion into the words.

## Deck format

Markdown slides separated by `---` lines (Marp or Slidev). The narration is
each slide's HTML comment, `<!-- Like this. -->`. The episode folder gets one
clip per slide, `deck.mp3` with a chapter per slide, `timings.json` and
`player.html`, which shows each slide while its clip plays and moves on.

## Rules

- Write for the ear: short sentences, numbers as you would say them, no
  tables or code. Markdown marks and links are stripped before speaking.
- One render runs at a time; others wait (`--wait`, default 15 minutes). Do
  not start renders in parallel.
- tts-axi protects memory: it stops on critical memory pressure, any pressure
  rise or fast swap growth, retries once on the CPU, and otherwise fails and
  writes nothing. Report a failure; do not retry in a loop.
- The `engine` field says what spoke: `ane` (Neural Engine), `cpu`, or
  `ptts-cpu` (Portuguese and French). A `fallback` field says why the Neural
  Engine was not used.
- Portuguese and French need the `phonon-40m-multilingual` model, which
  `tts-axi doctor --fix` downloads once with no account or token
  (`tts-axi doctor` says whether it is there).
- Tell the user where the audio is (`path`) and that `index.html` in the
  library plays everything.

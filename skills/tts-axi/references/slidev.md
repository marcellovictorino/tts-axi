# Narrated Slidev decks

`tts-axi deck slides.md --slidev` speaks a [Slidev](https://sli.dev) deck's
presenter notes and writes a copy of the deck whose clicks follow the voice.
`--video` (which implies `--slidev`) also records it to an MP4 in a hidden
Chrome. The source deck is never changed.

**First:** if the user has a personal Slidev preferences skill (for example
`slidev-preferences`), read it before writing or editing slides. Its choices
of effects, look and pacing win over the defaults here.

## Setup

`tts-axi doctor --fix` installs Slidev 53.1.0 (with the `default` and
`seriph` themes) and Chrome for Testing into `~/.tts-axi`, pinned. It needs
Node.js (`brew install node`); `tts-axi doctor` shows a `slidev video` row.
Plain `tts-axi deck` (no `--slidev`) works without any of this.

## Notes and [click]

Each slide's narration is its last HTML comment, when the comment ends the
slide. `[click]` splits it into steps: the words before the first `[click]`
play when the slide opens, and the words after the k-th `[click]` play after
the slide's k-th click.

```markdown
# How it works

<div v-click="1">Notes, split at each click</div>
<div v-click="2">One clip per step</div>

<!--
Here is how it works.
[click] Each slide's notes are split at every click marker.
[click] Every step is spoken as its own clip, so the timing is exact.
-->
```

- One `[click]` per click the slide has. Count `v-click`, `v-after` does not
  add one, `v-clicks` adds one per item, code highlight steps `{1|2|3}` add
  one per step after the first.
- Use numbered clicks (`v-click="2"`) so each element's click is explicit.
  `[click:3]` in notes is refused.
- Hidden slides (`hide: true`) are skipped. `src:` imports and frontmatter in
  a yaml code block are refused: copy the slides in and use a `---` block.
- Write the notes for the ear: short sentences, numbers as you would say them.

tts-axi speaks each step as its own clip, leaves 0.7 s of silence before each
click's words and fires the click 0.1 s into it. It writes `start`, `cues`
and `clicks` into each slide's frontmatter in the copy.

## What the copy adds

The episode's `slidev/` folder is the deck plus tts-axi's layer. Do not add
files with these names to your deck folder; tts-axi refuses to overwrite them.

| File | What it does |
|---|---|
| `global-top.vue` | The narration player: plays `public/deck.mp3` and moves the clicks to match. `p` plays or pauses; arrow keys move the narration. |
| `slide-top.vue` | The laser pointer (below). |
| `components/CountUp.vue` | `<CountUp :to="22" :at="1" />` counts up when click 1 arrives, once; going back shows the final value. |
| `components/PiTerminal.vue` | A scripted coding-agent terminal session, played by clicks (below). Fixed width, so long lines never widen it. |

CSS classes, styled by the layer (set `--tts-accent` and `--tts-mark` in
your `style.css` to recolour them):

- Marks: `<span class="mk hl" :class="{ on: $clicks >= 2 }">words</span>`, with
  `hl` (highlight), `under`, `ring`, `box` or `strike`. Use these rather than
  `v-mark`, which misplaces a mark that appears on the same click as its
  parent.
- Spotlight: `class="spot" :class="{ off: $clicks >= 2 }"` dims a card so the
  one being discussed stands out.
- Bars: `<div class="bar" :style="{ width: $clicks >= 3 ? '72%' : '0%' }" />`
  grows to its value.

## Laser

Give an element `data-laser="<click>"` (space-separate several clicks) and
optionally `data-mode="sweep|circle|point"` (sweep is the default). When that
click arrives a red dot glides there, underlines, circles or rests on it, and
fades about 1.5 s after its gesture.

```html
<span data-laser="2" data-mode="circle">One clip per step</span>
```

Keep it rare: one moment on two or three slides of a deck.

## Terminal emulation

```html
<PiTerminal :at="0" command="pi" question="What changed in this repo today?"
  tool="git log --since=midnight --oneline" :tool-output="['a1b2 fix login', 'c3d4 docs']"
  :answer="['Two commits today:', '• a login fix', '• a docs update']" />
```

Clicks from `at`: +0 types the command, +1 opens the agent, +2 types and
sends the question, +3 runs the tool, +4 streams the answer at 30 characters
a second. The narration sets how long the agent works: leave about 6 s of
words between the question and the tool click, and about 4 s between the tool
and the answer. Say on the slide that the session is scripted.

## Pacing defaults

- One idea per slide, two to six clicks, about 10 to 30 s of narration.
- One step's words per element: say what appears as it appears.
- Avoid `transition: view-transition`; `--video` refuses it because it freezes
  the recording. `slide-left`, `slide-up` and `fade` record well.
- Themes: `default`, `seriph`, `none`, or a theme folder by path.

## Recording

```bash
tts-axi deck slides.md --video
```

tts-axi speaks the deck, then plays the narrated copy in a hidden Chrome for
Testing (1280 x 720, on the wall clock), captures every frame with its time,
and joins the frames with the narration. The episode folder gets:

- `deck.mp4`: H.264 and AAC, chapter marks per slide.
- `chapters.txt`: `0:00 Title` lines for a YouTube description.
- `captions.srt`: one caption per spoken sentence.
- `slidev/`: the narrated deck. To present it live:
  `cd slidev && npm ci && npm run dev`, then press `p`.

Recording takes as long as the narration, plus the time Slidev takes to
start. If memory pressure rises, tts-axi stops and writes nothing.

## Sharing

- **Unlisted YouTube video:** upload `deck.mp4` as unlisted, paste
  `chapters.txt` into the description and add `captions.srt` as subtitles.
  Embed it without tracking cookies:

  ```html
  <iframe width="1280" height="720" src="https://www.youtube-nocookie.com/embed/VIDEO_ID"
    title="Talk title" allow="encrypted-media; picture-in-picture" allowfullscreen></iframe>
  ```

- **Self-hosted MP4:** put `deck.mp4` and `captions.srt` (converted to WebVTT,
  `ffmpeg -i captions.srt captions.vtt`) on any web host:

  ```html
  <video controls preload="metadata" width="1280" src="deck.mp4">
    <track kind="captions" src="captions.vtt" srclang="en" label="English" default>
  </video>
  ```

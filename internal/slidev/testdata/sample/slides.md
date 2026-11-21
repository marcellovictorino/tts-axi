---
theme: default
title: Narrated Slidev decks
colorSchema: dark
transition: slide-left
layout: center
class: text-center
---

<div class="eyebrow">tts-axi · deck --slidev</div>

# Slides that <span class="mk hl" :class="{ on: $clicks >= 2 }">talk for themselves</span>

<p v-click="1" class="muted text-xl">Write the notes once. Get a narrated video.</p>

<!--
tts-axi can narrate a Slidev deck.
[click] You write the notes once, and you get a narrated video.
[click] The slides talk for themselves.
-->

---

# How it works

<div class="flex flex-col gap-4 text-xl mt-6">
  <div v-click="1">1 · Notes, split at each click</div>
  <div v-click="2"><span data-laser="2" data-mode="circle">2 · One clip per step</span></div>
  <div v-click="3">3 · Cues written into the deck</div>
</div>

<!--
Here is how it works.
[click] Each slide's notes are split at every click marker.
[click] Every step is spoken as its own clip, so the timing is exact.
[click] The click times are then written back into a copy of the deck.
-->

---

# Three ways to share

<div class="grid grid-cols-3 gap-4 mt-8">
  <div v-click="1" class="panel spot" :class="{ off: $clicks >= 2 }">Slidev page</div>
  <div v-click="2" class="panel spot" :class="{ off: $clicks >= 3 }">MP4 file</div>
  <div v-click="3" class="panel spot"><span class="mk under" :class="{ on: $clicks >= 4 }">Unlisted video</span></div>
</div>

<!--
There are three ways to share it.
[click] The live Slidev page.
[click] The MP4 file itself.
[click] Or an unlisted video, with chapters and captions.
[click] For a quick link, the unlisted video is the easiest.
-->

---

# Numbers that grow

<div v-click="1" class="text-6xl font-bold mt-6"><CountUp :to="22" :at="1" />/22</div>
<div v-click="2" class="mt-8 w-120">
  <div class="muted text-sm mb-1">tests passed</div>
  <div class="bar" :style="{ width: $clicks >= 2 ? '100%' : '0%' }" />
</div>
<p v-click="3" class="mt-6"><span class="mk ring" :class="{ on: $clicks >= 3 }">every one</span> of them</p>

<!--
Numbers can grow on screen.
[click] Twenty two out of twenty two.
[click] Bars slide out to their value.
[click] And a ring marks the point that matters.
-->

---

# Step through commands

```bash {1|2|3}
tts-axi doctor --fix
tts-axi deck slides.md --slidev
tts-axi deck slides.md --video
```

<!--
Commands can be stepped through, one line at a time. First, doctor fix installs what is needed.
[click] Then deck with slidev writes the narrated deck.
[click] And video records it.
-->

---
layout: center
title: Terminal demos
---

<PiTerminal :at="0" command="pi" />

<p v-click="5" class="mt-4 text-center"><span data-laser="5" data-mode="sweep" class="muted">all scripted, nothing runs</span></p>

<!--
A terminal demo is an emulation.
[click] The agent opens.
[click] The question is typed.
[click] A tool runs.
[click] And the answer streams in, at a local model's pace.
[click] Everything is scripted, so nothing runs for real.
-->

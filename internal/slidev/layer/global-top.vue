<script setup lang="ts">
// tts-axi narration player. The deck's audio is the clock: each slide's
// frontmatter gives `start` (seconds into the audio) and `cues` (seconds
// after the slide opens at which each click fires), written by
// `tts-axi deck --slidev`. Press p or the button to play; arrows and clicks
// move the narration to match. ?clock=1 runs on the wall clock instead of
// the audio (for recording), ?chrome=0 hides the button.
import { computed, onMounted, ref, watch } from 'vue'
import { useNav } from '@slidev/client'

const total = __TTS_AXI_TOTAL__ // seconds of narration
const nav = useNav()
const audio = ref<HTMLAudioElement>()
const playing = ref(false)
const time = ref(0)
const href = window.location.href
const clockMode = /[?&]clock=1/.test(href)
const hideUi = /[?&]chrome=0/.test(href)
const src = `${import.meta.env.BASE_URL}__TTS_AXI_AUDIO__`
let t0 = 0
let pushed = { no: -1, clicks: -1 }

const timeline = computed(() => nav.slides.value.map((r: any) => {
  const fm: any = r.meta?.slide?.frontmatter ?? {}
  return { no: r.no, start: Number(fm.start ?? 0), cues: (fm.cues ?? []) as number[], timed: fm.start !== undefined }
}).filter(x => x.timed))

function positionAt(t: number) {
  let s = timeline.value[0]
  for (const x of timeline.value) if (x.start <= t) s = x
  return { no: s.no, clicks: s.cues.filter(c => s.start + c <= t).length }
}
function timeOf(no: number, clicks: number) {
  const s = timeline.value.find(x => x.no === no)
  if (!s) return 0
  return s.start + (clicks > 0 ? (s.cues[clicks - 1] ?? 0) : 0)
}
function now() {
  return clockMode ? (performance.now() - t0) / 1000 : (audio.value?.currentTime ?? 0)
}
function seek(t: number) {
  time.value = t
  if (clockMode) t0 = performance.now() - t * 1000
  else if (audio.value) audio.value.currentTime = t
}
function tick() {
  if (!playing.value) return
  const t = now()
  time.value = t
  if (t >= total) {
    stop()
    ;(window as any).__narrationDone = true
    return
  }
  const p = positionAt(t)
  if (p.no !== nav.currentSlideNo.value || p.clicks !== nav.clicks.value) {
    pushed = p
    nav.go(p.no, p.clicks)
  }
  requestAnimationFrame(tick)
}
function play() {
  if (!timeline.value.length) return
  // Resume where it paused if that is still on the step shown; otherwise,
  // or once the narration has finished, start the step shown from its
  // beginning.
  const here = positionAt(time.value)
  if (time.value >= total || here.no !== nav.currentSlideNo.value || here.clicks !== nav.clicks.value) seek(timeOf(nav.currentSlideNo.value, nav.clicks.value))
  else seek(time.value)
  playing.value = true
  if (!clockMode) audio.value?.play().catch(() => { playing.value = false })
  requestAnimationFrame(tick)
}
function stop() {
  playing.value = false
  audio.value?.pause()
}
function toggle() { playing.value ? stop() : play() }

// Manual navigation (arrows, clicks) moves the narration to match.
watch(() => [nav.currentSlideNo.value, nav.clicks.value], ([no, clicks]) => {
  if (no === pushed.no && clicks === pushed.clicks) return
  pushed = { no: -1, clicks: -1 }
  if (timeline.value.length) seek(timeOf(no as number, clicks as number))
})

onMounted(() => {
  // The recorder lines frames up against this clock: return when it started.
  ;(window as any).__narrate = () => { const at = Date.now() / 1000; play(); return at }
  window.addEventListener('keydown', e => { if (e.key === 'p') toggle() })
})
const pct = computed(() => `${Math.min(100, (time.value / total) * 100)}%`)
</script>

<template>
  <audio v-if="!clockMode" ref="audio" :src="src" preload="auto" />
  <div v-if="!hideUi" class="tts-narr" :class="{ on: playing }">
    <button @click="toggle">{{ playing ? '❚❚ Narrating' : '▶ Play narration' }}</button>
    <div class="track"><div class="fill" :style="{ width: pct }" /></div>
  </div>
</template>

<style scoped>
.tts-narr { position: fixed; left: 14px; bottom: 14px; z-index: 100; display: flex; align-items: center; gap: 10px;
  background: rgba(10,14,26,.8); border: 1px solid rgba(255,255,255,.18); border-radius: 999px; padding: 6px 12px; backdrop-filter: blur(6px); }
.tts-narr button { color: #f2f5fa; font: 600 13px/1 var(--slidev-theme-font-sans, sans-serif); background: none; border: 0; cursor: pointer; }
.track { width: 140px; height: 3px; background: rgba(255,255,255,.12); border-radius: 2px; overflow: hidden; }
.fill { height: 100%; background: var(--tts-accent, #5ac8fa); }
</style>

<style>
/* tts-axi marks, driven by $clicks so they never misplace during autoplay
   (Slidev's v-mark measures a parent that appears on the same click wrongly).
   <span class="mk under" :class="{ on: $clicks >= 2 }">words</span>
   Kinds: hl (highlight), under, ring, box, strike. Colours follow
   --tts-accent and --tts-mark. */
.mk { position: relative; display: inline-block; z-index: 0; }
.mk::after { content: ''; position: absolute; pointer-events: none; transition: all .6s cubic-bezier(.2,.8,.2,1); }
.mk.hl::after { left: -4px; top: 8%; height: 84%; width: 0; background: var(--tts-accent, #5ac8fa); opacity: .28; border-radius: 4px; z-index: -1; }
.mk.hl.on::after { width: calc(100% + 8px); }
.mk.under::after { left: 0; bottom: -4px; height: 3px; width: 0; background: var(--tts-accent, #5ac8fa); border-radius: 2px; }
.mk.under.on::after { width: 100%; }
.mk.ring::after { inset: -6px -10px; border: 2px solid var(--tts-mark, #ff6b6b); border-radius: 999px; opacity: 0; transform: scale(1.4); }
.mk.ring.on::after { opacity: 1; transform: scale(1); }
.mk.box::after { inset: -3px -6px; border: 2px solid var(--tts-mark, #ffa94d); border-radius: 6px; opacity: 0; transform: scale(1.4); }
.mk.box.on::after { opacity: 1; transform: scale(1); }
.mk.strike::after { left: -4px; top: 52%; height: 2px; width: 0; background: var(--tts-mark, #ff6b6b); }
.mk.strike.on::after { width: calc(100% + 8px); transition-delay: .45s; }
/* Spotlight: dim the items not being talked about.
   <div class="spot" :class="{ off: $clicks >= 1 && $clicks !== 2 }">...</div> */
.spot { transition: opacity .5s ease, filter .5s ease; }
.spot.off { opacity: .28; filter: saturate(.4); }
/* Growing bar: <div class="bar" :style="{ width: $clicks >= 3 ? '72%' : '0%' }" /> */
.bar { height: 14px; border-radius: 4px; background: var(--tts-accent, #5ac8fa); transition: width 1.2s cubic-bezier(.2,.8,.2,1); }
</style>

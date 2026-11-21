<script setup lang="ts">
// tts-axi laser pointer, one per slide. An element carries data-laser="<click>"
// (space-separated for several clicks) and optional data-mode="sweep|circle|point".
// When the slide's click count reaches that number, a red dot glides there and
// underlines, circles or rests on it, with a slight hand tremor and a fading trail,
// then fades out about 1.5 s after its gesture. Use it sparingly: one moment on
// two or three slides of a deck.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useIsSlideActive, useSlideContext } from '@slidev/client'

const { $clicks } = useSlideContext()
const active = useIsSlideActive()
const root = ref<HTMLElement>()
const pos = ref({ x: -100, y: -100 })
const on = ref(false)
const trail = ref<{ x: number, y: number }[]>([])
let path: ((t: number) => { x: number, y: number }) | null = null
let t0 = 0, raf = 0, last = { x: 490, y: 300 }, hideTimer = 0
const shown = ref(false) // fades the dot and trail out after each gesture
const HOLD = 1400 // ms the dot rests after its gesture before fading (.6 s fade)

function rectOf(el: Element) {
  const box = root.value!.getBoundingClientRect()
  const scale = box.width / root.value!.offsetWidth || 1
  const r = el.getBoundingClientRect()
  return { x: (r.left - box.left) / scale, y: (r.top - box.top) / scale, w: r.width / scale, h: r.height / scale }
}
const ease = (k: number) => k < .5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2
const lerp = (a: any, b: any, k: number) => ({ x: a.x + (b.x - a.x) * k, y: a.y + (b.y - a.y) * k })

function aim(clicks: number, tries = 0) {
  // the slide body can mount after this layer, so look again briefly before giving up
  const el = root.value?.parentElement?.querySelector(`[data-laser~="${clicks}"]`)
  if (!el) {
    if (tries < 10) setTimeout(() => { if ($clicks.value === clicks && active.value) aim(clicks, tries + 1) }, 60)
    else shown.value = false
    return
  }
  setTimeout(() => {
    // the audience may have moved on, or the slide unmounted, in the meantime
    if (!root.value || !active.value || $clicks.value !== clicks) return
    const r = rectOf(el), mode = (el as HTMLElement).dataset.mode || 'sweep'
    const from = shown.value ? { ...last } : { x: r.x - 60, y: r.y + r.h + 40 }
    const move = 450
    const cx = r.x + r.w / 2, cy = r.y + r.h / 2
    let after: (t: number) => { x: number, y: number }
    if (mode === 'circle') {
      const rx = r.w / 2 + 14, ry = r.h / 2 + 10, d = 1300
      const start = { x: cx - rx, y: cy }
      const rest = { x: cx + Math.min(r.w * .25, 30), y: r.y + r.h + 8 }
      after = t => {
        if (t > d) return lerp({ x: cx - rx, y: cy }, rest, ease(Math.min(1, (t - d) / 350)))
        const a = Math.PI + ease(t / d) * Math.PI * 2
        return { x: cx + rx * Math.cos(a), y: cy + ry * Math.sin(a) }
      }
      path = mk(from, start, move, after)
    } else if (mode === 'point') {
      const p = { x: r.x + Math.min(r.w * .5, 60), y: r.y + r.h * .92 }
      path = mk(from, p, move, () => p)
    } else {
      const y = r.y + r.h * .78, a = { x: r.x, y }, b = { x: r.x + r.w, y }, d = 300 + r.w * 2.2
      path = mk(from, a, move, t => lerp(a, b, ease(Math.min(1, t / d))))
    }
    const gesture = mode === 'circle' ? 1300 + 350 : mode === 'point' ? 900 : 300 + r.w * 2.2
    on.value = true
    shown.value = true
    t0 = performance.now()
    clearTimeout(hideTimer)
    // used sparingly: point, hold a moment, then let go
    hideTimer = window.setTimeout(() => { shown.value = false }, move + gesture + HOLD)
  }, 120)
}
function mk(from: any, to: any, move: number, after: (t: number) => any) {
  return (t: number) => t < move ? lerp(from, to, ease(t / move)) : after(t - move)
}
function frame(now: number) {
  if (path) {
    const p = path(now - t0)
    const j = { x: Math.sin(now / 97) * .9 + Math.sin(now / 41) * .5, y: Math.cos(now / 83) * .9 + Math.sin(now / 53) * .4 }
    last = p
    pos.value = { x: p.x + j.x, y: p.y + j.y }
    trail.value = [pos.value, ...trail.value].slice(0, 12)
  }
  raf = requestAnimationFrame(frame)
}
watch([$clicks, active], ([c, a]) => { if (a) aim(c as number); else { on.value = false; shown.value = false } })
onMounted(() => { raf = requestAnimationFrame(frame); if (active.value) aim($clicks.value) })
onBeforeUnmount(() => cancelAnimationFrame(raf))
const dots = computed(() => trail.value.slice(1).map((p, i) => ({ ...p, o: .35 * (1 - i / 11), r: 5 - i * .3 })))
</script>

<template>
  <div ref="root" class="absolute inset-0 pointer-events-none" style="z-index: 50">
    <div v-if="on" class="laser-wrap" :style="{ opacity: shown ? 1 : 0 }">
      <div v-for="(d, i) in dots" :key="i" class="laser-trail" :style="{ left: d.x + 'px', top: d.y + 'px', opacity: d.o, width: d.r * 2 + 'px', height: d.r * 2 + 'px' }" />
      <div class="laser" :style="{ left: pos.x + 'px', top: pos.y + 'px' }" />
    </div>
  </div>
</template>

<style scoped>
.laser { position: absolute; width: 15px; height: 15px; margin: -7.5px 0 0 -7.5px; border-radius: 50%;
  background: radial-gradient(circle, #fff 0 18%, #ff2a2a 40%, rgba(255,0,0,.9) 60%, rgba(255,0,0,0) 72%);
  box-shadow: 0 0 10px 4px rgba(255,30,30,.75), 0 0 26px 10px rgba(255,0,0,.35); }
.laser-wrap { position: absolute; inset: 0; transition: opacity .6s ease; }
.laser-trail { position: absolute; transform: translate(-50%, -50%); border-radius: 50%; background: #ff2a2a; filter: blur(1.5px); }
</style>

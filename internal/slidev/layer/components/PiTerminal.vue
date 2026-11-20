<script setup lang="ts">
// tts-axi terminal emulation: a scripted coding-agent session in a terminal
// window, played step by step with the slide's clicks. Nothing runs; every
// line is written in advance, so a demo never waits on a slow model.
//   click at+0: the command is typed at the shell prompt
//   click at+1: the agent opens
//   click at+2: the question is typed and sent; the agent works
//   click at+3: a tool runs and prints its output
//   click at+4: the answer streams in
// The window has a fixed width, so a long line never widens it past the slide,
// and the cursor blinks in place without moving the text. How long the agent
// works is the narration's pace: leave about 6 s between the question and the
// tool click, and about 4 s between the tool and the answer.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useSlideContext } from '@slidev/client'

const props = withDefaults(defineProps<{
  at?: number
  title?: string
  cwd?: string
  command?: string
  banner?: string[]
  question?: string
  tool?: string
  toolOutput?: string[]
  answer?: string[]
  model?: string
  width?: number
  height?: number
  thinking?: number
  answerCps?: number
}>(), {
  at: 0,
  title: '~/demo — zsh',
  cwd: '~/demo',
  command: 'pi',
  banner: () => ['pi · local model', 'escape to interrupt · ctrl+c to clear · / for commands'],
  question: 'What is the weather forecast for this week?',
  tool: `curl -s 'wttr.in/Paris?format=j1' | jq -r '.weather[] | …'`,
  toolOutput: () => ['Fri  6-11C  light rain', 'Sat  4-9C   partly cloudy', 'Sun  3-8C   sunny'],
  answer: () => ['Paris, next three days:', '', '• Fri  6–11 °C  light rain, take an umbrella', '• Sat  4–9 °C   partly cloudy', '• Sun  3–8 °C   sunny and cold'],
  model: 'local model',
  width: 880,
  height: 400,
  thinking: 500, // ms after the tool click before its first output line
  answerCps: 30, // characters a second: a small local model's unhurried pace
})
const { $clicks } = useSlideContext()
const step = computed(() => $clicks.value - props.at) // the phase, 0 to 4
const now = ref(performance.now())
const since = ref<number[]>([performance.now(), 0, 0, 0, 0])
let raf = 0
const loop = () => { now.value = performance.now(); raf = requestAnimationFrame(loop) }
onMounted(() => { raf = requestAnimationFrame(loop) })
onBeforeUnmount(() => cancelAnimationFrame(raf))
watch(step, (c, p) => { if (c >= 0 && c <= 4 && c > (p ?? -1)) since.value[c] = performance.now() }, { immediate: true })
const t = (phase: number) => step.value < phase ? -1 : now.value - (since.value[phase] || now.value)
const typed = (s: string, phase: number, delay = 0, cps = 22) => {
  const e = t(phase) - delay
  return e < 0 ? '' : s.slice(0, Math.floor(e / 1000 * cps))
}
const spin = computed(() => '⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'[Math.floor(now.value / 90) % 10])
const blink = computed(() => Math.floor(now.value / 530) % 2 === 0)
const streamed = computed(() => {
  const e = t(4)
  if (e < 0) return []
  let n = Math.floor(e / 1000 * props.answerCps)
  const out: string[] = []
  for (const l of props.answer) {
    if (n <= 0) break
    out.push(l.slice(0, n))
    n -= l.length + 1
  }
  return out
})
const commandTyped = computed(() => typed(props.command, 0, 500, 8))
const inAgent = computed(() => step.value >= 1)
const questionTyped = computed(() => typed(props.question, 2, 0, 24))
const sent = computed(() => t(2) > props.question.length / 24 * 1000 + 400)
const working = computed(() => sent.value && step.value < 4)
const usage = computed(() => step.value >= 4 ? (t(4) > 1500 ? '6.8%' : '4.1%') : sent.value ? '2.9%' : '0.0%')
</script>

<template>
  <div class="win" :style="{ width: width + 'px', maxWidth: width + 'px', height: height + 'px' }">
    <div class="bar"><i style="background:#ff5f57" /><i style="background:#febc2e" /><i style="background:#28c840" /><span>{{ title }}</span></div>
    <div class="scr">
      <template v-if="!inAgent">
        <div><span class="dim">{{ cwd }}</span> <span class="acc">❯</span> {{ commandTyped }}<span class="cur" :class="{ off: !blink }">▍</span></div>
      </template>
      <template v-else>
        <div v-for="(l, i) in banner" :key="'b' + i" class="dim">{{ l }}</div>
        <div class="grow">
          <div v-if="sent" class="user">{{ question }}</div>
          <div v-if="step >= 3" class="tool">
            <div class="tcmd">$ {{ tool }}</div>
            <div v-for="(l, i) in toolOutput" v-show="t(3) > thinking + i * 180" :key="i" class="tout">{{ l }}</div>
          </div>
          <div v-for="(l, i) in streamed" :key="'a' + i" class="ans">{{ l || ' ' }}</div>
          <div v-if="working" class="acc">{{ spin }} Working...</div>
        </div>
        <div class="rule" />
        <div class="ed">{{ sent ? '' : questionTyped }}<span class="cur" :class="{ off: !blink || working }">▍</span></div>
        <div class="rule" />
        <div class="dim">{{ cwd }}</div>
        <div class="foot"><span class="ok">{{ usage }}</span><span class="mod">{{ model }}</span></div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.win { min-width: 0; border-radius: 12px; overflow: hidden; background: #1e1e1e; box-shadow: 0 20px 60px rgba(0,0,0,.5), 0 0 0 1px rgba(255,255,255,.08); display: flex; flex-direction: column; text-align: left; }
.bar { height: 28px; flex-shrink: 0; background: #2b2b2b; display: flex; align-items: center; gap: 7px; padding: 0 12px; font: 12px/1 Inter, sans-serif; color: #9a9a9a; }
.bar i { width: 11px; height: 11px; border-radius: 50%; display: inline-block; }
.bar span { margin-left: 12px; }
.scr { flex: 1; min-width: 0; min-height: 0; overflow: hidden; padding: 10px 14px; font: 12.5px/1.45 'JetBrains Mono', Menlo, monospace; color: #d4d4d4; display: flex; flex-direction: column; }
.grow { flex: 1; min-width: 0; overflow: hidden; display: flex; flex-direction: column; justify-content: flex-end; gap: 4px; padding: 6px 0; }
.dim { color: #8a8a8a; } .acc { color: #61afef; } .ok { color: #98c379; } .mod { color: #c678dd; }
.cur { color: #d4d4d4; } .cur.off { visibility: hidden; }
.rule { height: 1px; background: #61afef; opacity: .8; margin: 2px 0; }
.ed { min-height: 20px; padding: 2px 0; white-space: nowrap; overflow: hidden; }
.foot { display: flex; justify-content: space-between; }
.user { background: #2d2f3a; padding: 4px 8px; border-radius: 4px; }
.tool { flex-shrink: 0; min-width: 0; overflow: hidden; background: #23302a; padding: 6px 8px; border-radius: 4px; }
.tcmd { color: #e5c07b; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tout { color: #b5c2b5; white-space: pre; }
.ans { white-space: pre; overflow: hidden; text-overflow: ellipsis; }
</style>

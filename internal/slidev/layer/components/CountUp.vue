<script setup lang="ts">
// tts-axi counter: counts from `from` to `to` when the slide reaches click `at`.
// It counts only the first time that click is reached; later clicks, or coming
// back, show the final number instead of starting again.
//   <CountUp :to="22" :at="3" />/22
import { onBeforeUnmount, ref, watch } from 'vue'
import { useSlideContext } from '@slidev/client'

const props = withDefaults(defineProps<{ to: number, at?: number, from?: number, duration?: number, decimals?: number }>(),
  { at: 0, from: 0, duration: 1200, decimals: 0 })
const { $clicks } = useSlideContext()
const value = ref(props.from)
let counted = false
let raf = 0

function count() {
  const t0 = performance.now()
  const step = (now: number) => {
    const k = Math.min(1, (now - t0) / props.duration)
    const eased = 1 - Math.pow(1 - k, 3)
    value.value = props.from + (props.to - props.from) * eased
    if (k < 1) raf = requestAnimationFrame(step)
  }
  raf = requestAnimationFrame(step)
}
watch($clicks, (c) => {
  if (c < props.at) {
    if (!counted) value.value = props.from
    return
  }
  if (counted) {
    cancelAnimationFrame(raf) // a later click ends a count still running
    value.value = props.to
    return
  }
  counted = true
  count()
}, { immediate: true })
onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>

<template>
  <span class="tabular-nums">{{ value.toFixed(decimals) }}</span>
</template>

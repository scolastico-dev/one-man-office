<script setup lang="ts">
// A vertical bar that sets the width of the area on its left: dragged with
// the pointer, or focused and moved with the arrow keys in 16 px steps, Home
// for the narrowest and End for the widest.
import { computed, ref } from 'vue'

const props = defineProps<{
  /** Width in px. */
  modelValue: number
  min: number
  max: number
  /** Accessible name, e.g. "Sidebar width". */
  label: string
}>()

const emit = defineEmits<{ 'update:modelValue': [px: number] }>()

const STEP = 16

const clamp = (v: number) => Math.round(Math.min(props.max, Math.max(props.min, v)))
const value = computed(() => clamp(props.modelValue))

function set(v: number) {
  const next = clamp(v)
  if (next !== value.value) emit('update:modelValue', next)
}

function onKeydown(e: KeyboardEvent) {
  let next: number
  if (e.key === 'ArrowRight') next = value.value + STEP
  else if (e.key === 'ArrowLeft') next = value.value - STEP
  else if (e.key === 'Home') next = props.min
  else if (e.key === 'End') next = props.max
  else return
  e.preventDefault()
  set(next)
}

const dragging = ref<{ pointer: number; x: number; start: number } | null>(null)

function onPointerdown(e: PointerEvent) {
  if (e.button !== 0) return
  e.preventDefault()
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
  dragging.value = { pointer: e.pointerId, x: e.clientX, start: value.value }
}

function onPointermove(e: PointerEvent) {
  const d = dragging.value
  if (d === null || e.pointerId !== d.pointer) return
  set(d.start + e.clientX - d.x)
}

function onPointerup(e: PointerEvent) {
  const d = dragging.value
  if (d === null || e.pointerId !== d.pointer) return
  ;(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId)
  dragging.value = null
}
</script>

<template>
  <div
    class="splitter"
    :class="{ 'splitter--dragging': dragging }"
    role="separator"
    aria-orientation="vertical"
    :aria-label="label"
    :aria-valuenow="value"
    :aria-valuemin="min"
    :aria-valuemax="max"
    :title="label"
    tabindex="0"
    @keydown="onKeydown"
    @pointerdown="onPointerdown"
    @pointermove="onPointermove"
    @pointerup="onPointerup"
    @pointercancel="onPointerup"
    @lostpointercapture="dragging = null"
  />
</template>

<style scoped>
/* A wide grip around a one-line rule; the rule takes the accent while held, hovered or focused. */
.splitter {
  position: relative;
  flex: none;
  width: calc(var(--omo-space) * 2);
  align-self: stretch;
  cursor: col-resize;
  touch-action: none;
}
.splitter::before {
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(50% - var(--omo-border) / 2);
  width: var(--omo-border);
  background: var(--omo-line);
  content: '';
  transition:
    background-color var(--omo-duration) ease,
    width var(--omo-duration) ease,
    left var(--omo-duration) ease;
}
.splitter:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.splitter:focus-visible::before,
.splitter--dragging::before {
  left: calc(50% - var(--omo-border));
  width: calc(var(--omo-border) * 2);
  background: var(--omo-accent);
}
@media (hover: hover) {
  .splitter:hover::before {
    left: calc(50% - var(--omo-border));
    width: calc(var(--omo-border) * 2);
    background: var(--omo-accent);
  }
}
</style>

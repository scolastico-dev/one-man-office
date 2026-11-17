<script setup lang="ts">
import { computed } from 'vue'
import StatusBadge from './StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    /** Colour and the word in front: Note, Warning or Problem, so colour is never the only signal. */
    tone: 'info' | 'warn' | 'danger'
    /** Shown after the tone word, e.g. the file a problem is about. */
    title?: string
    /** Make the note a live region: `polite` a status, `assertive` an alert. Leave unset for notes present from the start. */
    live?: 'polite' | 'assertive'
  }>(),
  { title: undefined, live: undefined },
)

const WORDS = { info: 'Note', warn: 'Warning', danger: 'Problem' } as const
const BADGE_TONES = { info: 'accent', warn: 'warn', danger: 'danger' } as const

const role = computed(() => (props.live === 'assertive' ? 'alert' : props.live === 'polite' ? 'status' : 'note'))
</script>

<template>
  <div class="callout" :class="`callout--${tone}`" :data-tone="tone" :role="role">
    <p class="callout__head">
      <StatusBadge :tone="BADGE_TONES[tone]" :text="WORDS[tone]" data-tone-word />
      <span v-if="title" class="callout__title">{{ title }}</span>
    </p>
    <div class="callout__body"><slot /></div>
    <div v-if="$slots.actions" class="callout__actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
/* A note with a coloured edge on its left, like a story card, and its tone in words. */
.callout {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  padding: calc(var(--omo-space) * 2.5) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--callout-tone);
  border-left-width: var(--omo-edge);
  background: var(--omo-surface);
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.callout--info {
  --callout-tone: var(--omo-accent);
}
.callout--warn {
  --callout-tone: var(--omo-warn);
}
.callout--danger {
  --callout-tone: var(--omo-danger);
  background: var(--omo-danger-soft);
}
.callout__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
}
.callout__title {
  min-width: 0;
  font-weight: 700;
  overflow-wrap: anywhere;
}
.callout__body {
  min-width: 0;
  overflow-wrap: anywhere;
}
.callout__body :deep(p) {
  margin: 0;
}
.callout__body :deep(p + p) {
  margin-top: calc(var(--omo-space) * 1.5);
}
.callout__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
  margin-top: var(--omo-space);
}
</style>

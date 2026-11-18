<script setup lang="ts">
// A text to copy (an address, a command, a token's secret once) with its
// Copy button. Whether the copy worked is said next to the button, never in
// a toast; a refused copy asks to select the text instead.
import { ref, useId } from 'vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleLines } from '../../../components/ui/visibleText'
import { writeClipboard } from '../../terminal/terminalClipboard'

const props = defineProps<{
  /** What is copied, also the name of the box and the button ("the address"). */
  label: string
  text: string
}>()

const announce = useAnnouncer()
const said = ref('')
const id = useId()

async function copy() {
  const ok = await writeClipboard(props.text)
  said.value = ok ? 'Copied.' : 'The browser did not let omo copy; select the text and copy it.'
  announce.polite(said.value)
}
</script>

<template>
  <div class="copy">
    <pre :id="id" class="copy__text" tabindex="0" :aria-label="label">{{ visibleLines(text) }}</pre>
    <div class="copy__bar">
      <UiButton size="sm" :aria-describedby="id" data-copy @click="copy">Copy<span class="sr-only"> {{ label }}</span></UiButton>
      <span v-if="said" class="copy__said" data-copy-said>{{ said }}</span>
    </div>
  </div>
</template>

<style scoped>
.copy {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.copy__text {
  margin: 0;
  padding: calc(var(--omo-space) * 2);
  overflow-x: auto;
  /* A composited layer: the compositor then finds this scroll box under a wheel itself, where asking the main thread can drop the wheel. */
  will-change: transform;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.copy__text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.copy__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.copy__said {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>

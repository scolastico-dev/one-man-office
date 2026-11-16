<script setup lang="ts">
// Stands in the place of a terminal that is open in its own window: the same
// box, quiet, with the ways to bring the terminal back or to find its window.
// It holds no connection, so the window is the terminal's only viewer and
// sets its size.
import { onMounted } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { usePopouts } from './popout'
import type { TerminalTarget } from './terminalTarget'

const props = withDefaults(defineProps<{ target: TerminalTarget; label: string; fill?: boolean }>(), { fill: false })

const popouts = usePopouts()
const announcer = useAnnouncer()

onMounted(() => announcer.polite(`${props.label}: open in its own window.`))
</script>

<template>
  <div class="tvo" :class="{ 'tvo--fill': fill }" role="group" :aria-label="label" data-terminal-out>
    <div class="tvo__bar">
      <span class="tvo__state">In its own window</span>
    </div>
    <div class="tvo__screen">
      <div class="tvo__rows" aria-hidden="true"><span /><span /><span /></div>
      <p class="tvo__text">This terminal is open in its own window.</p>
      <div class="tvo__actions">
        <UiButton size="sm" data-terminal-bring-back @click="popouts.bringBack(target)">Bring it back</UiButton>
        <UiButton size="sm" data-terminal-show-window @click="popouts.show(target)">Show the window</UiButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Laid out like the terminal view it stands in for, so nothing moves when it swaps in. */
.tvo {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.tvo--fill {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
}
.tvo__bar {
  display: flex;
  align-items: center;
  min-height: calc(var(--omo-space) * 7);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tvo__screen {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  min-height: calc(var(--omo-space) * 60);
  padding: calc(var(--omo-space) * 4);
  border: var(--omo-border) dashed var(--omo-line-strong);
  background: var(--omo-surface);
  text-align: center;
}
.tvo--fill > .tvo__screen {
  flex: 1 1 0;
}
/* A few faint lines where the terminal's text would be. */
.tvo__rows {
  display: grid;
  justify-items: center;
  gap: calc(var(--omo-space) * 1.5);
  width: min(100%, calc(var(--omo-space) * 60));
}
.tvo__rows span {
  display: block;
  width: 100%;
  height: calc(var(--omo-space) * 1.5);
  background: var(--omo-line);
}
.tvo__rows span:nth-child(2) {
  width: 80%;
}
.tvo__rows span:nth-child(3) {
  width: 55%;
}
.tvo__text {
  margin: 0;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tvo__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: calc(var(--omo-space) * 2);
}
</style>

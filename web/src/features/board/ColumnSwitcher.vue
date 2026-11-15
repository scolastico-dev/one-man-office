<script setup lang="ts">
import type { Column } from '../../api/types'
import type { ColumnModel } from './model'

defineProps<{ columns: ColumnModel[]; active: Column }>()
const emit = defineEmits<{ select: [column: Column] }>()
</script>

<template>
  <div class="switcher" role="tablist" aria-label="Columns">
    <button
      v-for="c in columns"
      :key="c.column"
      type="button"
      role="tab"
      class="switcher__tab"
      :class="`switcher__tab--${c.dropState}`"
      :aria-selected="c.column === active ? 'true' : 'false'"
      :aria-disabled="c.dropState === 'refused' ? 'true' : undefined"
      :title="c.dropState === 'refused' ? c.reason : undefined"
      :data-drop-column="c.column"
      @click="emit('select', c.column)"
    >
      <span class="switcher__name">{{ c.title }}</span>
      <span class="switcher__count">{{ ` ${c.count}` }}</span>
    </button>
  </div>
</template>

<style scoped>
.switcher {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.switcher__tab {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0;
  min-width: 0;
  min-height: calc(var(--omo-space) * 12);
  padding: var(--omo-space);
  border: 0;
  border-left: var(--omo-border) solid var(--omo-line);
  background: transparent;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: 1.4;
  text-align: center;
  cursor: pointer;
}
.switcher__tab:first-child {
  border-left: 0;
}
.switcher__tab[aria-selected='true'] {
  background: var(--omo-select);
  box-shadow: inset 0 calc(var(--omo-border) * -2) 0 var(--omo-accent);
  color: var(--omo-fg);
}
.switcher__tab:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.switcher__name {
  overflow-wrap: anywhere;
}
.switcher__count {
  color: var(--omo-accent);
  font-variant-numeric: tabular-nums;
}
.switcher__tab--allowed {
  outline: var(--omo-border) dashed var(--omo-accent);
  outline-offset: calc(var(--omo-border) * -3);
}
.switcher__tab--over {
  background: var(--omo-accent-soft);
  outline: var(--omo-border) solid var(--omo-accent);
  outline-offset: calc(var(--omo-border) * -3);
}
.switcher__tab--refused {
  background: repeating-linear-gradient(
    -45deg,
    var(--omo-line) 0 var(--omo-border),
    transparent var(--omo-border) calc(var(--omo-space) * 2)
  );
}
.switcher__tab--refused > span {
  opacity: 0.55;
}
@media (hover: hover) {
  .switcher__tab:hover {
    color: var(--omo-fg);
  }
}
</style>

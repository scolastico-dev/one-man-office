<script setup lang="ts">
import type { Column } from '../../api/types'
import { needsMe } from '../../board/filters'
import type { ColumnModel } from './model'

defineProps<{ columns: ColumnModel[]; active: Column }>()
const emit = defineEmits<{ select: [column: Column] }>()

/** A shown card of the column waits for the user: a question, attention or a review. */
function waits(c: ColumnModel): boolean {
  return c.cards.some((x) => needsMe(x.ticket))
}
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
      :title="c.dropState === 'refused' ? c.reason : c.action"
      :data-drop-column="c.column"
      @click="emit('select', c.column)"
    >
      <span class="switcher__name">{{ c.title }}</span>
      <span v-if="c.total !== undefined && c.total !== c.count" class="switcher__count">
        <span aria-hidden="true">{{ ` ${c.count}/${c.total}` }}</span>
        <span class="sr-only">{{ ` ${c.count} of ${c.total}` }}</span>
      </span>
      <span v-else class="switcher__count">{{ ` ${c.count}` }}</span>
      <span v-if="waits(c)" class="switcher__waits" data-waits title="Something here waits for you"
        ><span aria-hidden="true">!</span><span class="sr-only">, something here waits for you</span></span
      >
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
  position: relative;
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
.switcher__waits {
  position: absolute;
  top: var(--omo-space);
  right: var(--omo-space);
  min-width: calc(var(--omo-space) * 4);
  padding: 0 var(--omo-space);
  background: var(--omo-warn);
  color: var(--omo-bg);
  font-size: var(--omo-text-xs);
  font-weight: 700;
  line-height: var(--omo-text-xs-lh);
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

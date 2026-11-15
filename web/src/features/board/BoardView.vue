<script setup lang="ts">
import { ref } from 'vue'
import type { Column } from '../../api/types'
import BoardColumn from './BoardColumn.vue'
import ColumnSwitcher from './ColumnSwitcher.vue'
import type { ColumnModel } from './model'

const props = withDefaults(
  defineProps<{
    columns: ColumnModel[]
    /** Number of the card being dragged, shown as its placeholder. */
    draggingTicket?: number | null
    /** Column shown first on narrow screens. */
    initialColumn?: Column
  }>(),
  { draggingTicket: null, initialColumn: 'planning' },
)
const emit = defineEmits<{ open: [n: number]; 'filter-story': [n: number]; 'load-more': [] }>()

/** The one column shown below 768 px. */
const active = ref<Column>(props.initialColumn)
</script>

<template>
  <div class="board-view">
    <ColumnSwitcher class="board-view__switcher" :columns="columns" :active="active" @select="active = $event" />
    <div class="board" data-board role="region" aria-label="Board columns" tabindex="0">
      <BoardColumn
        v-for="c in columns"
        :key="c.column"
        :class="{ 'column--active': c.column === active }"
        :model="c"
        :dragging-ticket="draggingTicket"
        @open="emit('open', $event)"
        @filter-story="emit('filter-story', $event)"
        @load-more="emit('load-more')"
      />
    </div>
    <div v-if="$slots.empty" class="board__empty"><slot name="empty" /></div>
  </div>
</template>

<style scoped>
.board-view {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 2);
  height: 100%;
  min-width: 0;
  min-height: 0;
}
.board-view__switcher {
  display: none;
}
/* Columns keep a usable width; when the window (or the space next to the sidebar) is too narrow
   for five of them, the board scrolls sideways inside itself, never the page. */
.board {
  position: relative;
  display: grid;
  flex: 1 1 auto;
  grid-template-columns: repeat(5, minmax(15rem, 1fr));
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  min-height: 0;
  overflow-x: auto;
  overflow-y: hidden;
  scroll-snap-type: x mandatory;
  scroll-padding-inline: calc(var(--omo-space) * 2);
  /* Room above the panels for their labels, which sit on the top border. */
  padding-top: calc(var(--omo-space) * 2.5);
}
.board > :deep(.column) {
  scroll-snap-align: start;
}
.board__empty {
  position: absolute;
  z-index: 4;
  /* Below the columns' "No tickets", so the empty columns still read as columns. */
  inset: calc(var(--omo-space) * 24) calc(var(--omo-space) * 3) auto;
  display: flex;
  justify-content: center;
  pointer-events: none;
}
.board__empty > :deep(*) {
  width: min(100%, 36rem);
  pointer-events: auto;
}

/* Phones: one column at a time, chosen with the switcher. */
@media (max-width: 767.98px) {
  .board-view__switcher {
    display: grid;
    position: sticky;
    z-index: 5;
    top: 0;
  }
  .board {
    grid-template-columns: minmax(0, 1fr);
    overflow: visible;
    scroll-snap-type: none;
  }
  .board > :deep(.column:not(.column--active)) {
    display: none;
  }
  .board__empty {
    inset: calc(var(--omo-space) * 36) calc(var(--omo-space) * 2) auto;
  }
}
</style>

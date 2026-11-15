<script setup lang="ts">
import { computed, useId } from 'vue'
import Panel from '../../components/ui/Panel.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useCreateTicket } from '../create/useCreateTicket'
import TicketCard from './TicketCard.vue'
import type { CardModel, ColumnModel } from './model'

const props = withDefaults(
  defineProps<{
    model: ColumnModel
    /** Number of the card being dragged, shown as its placeholder. */
    draggingTicket?: number | null
  }>(),
  { draggingTicket: null },
)
const emit = defineEmits<{ 'load-more': []; open: [n: number]; 'filter-story': [n: number] }>()

const headingId = `column-${useId()}`
const create = useCreateTicket()

const groups = computed(() => {
  const stories: CardModel[] = []
  const tasks: CardModel[] = []
  for (const c of props.model.cards) (c.ticket.type === 'story' ? stories : tasks).push(c)
  return { stories, tasks }
})
/** Headings only help when both kinds share the column. */
const grouped = computed(() => props.model.column !== 'done' && groups.value.stories.length > 0 && groups.value.tasks.length > 0)
const refused = computed(() => props.model.dropState === 'refused')

/**
 * Where the drop line goes: the number of the card it touches and on which
 * side. Stories come before tasks, so a story dropped below every shown story
 * lands above the first task, and a task below every story lands last.
 */
const mark = computed((): { n: number; side: 'before' | 'after' } | null => {
  const m = props.model.marker
  if (!m) return null
  const cards = props.model.cards
  if (m.before !== null) return cards.some((c) => c.ticket.number === m.before) ? { n: m.before, side: 'before' } : null
  const same = cards.filter((c) => c.ticket.type === m.type)
  const last = same[same.length - 1]
  if (last) return { n: last.ticket.number, side: 'after' }
  if (m.type === 'story') {
    const first = cards[0]
    return first ? { n: first.ticket.number, side: 'before' } : null
  }
  const end = cards[cards.length - 1]
  return end ? { n: end.ticket.number, side: 'after' } : null
})

function markOf(c: CardModel): 'before' | 'after' | null {
  return mark.value?.n === c.ticket.number ? mark.value.side : null
}
</script>

<template>
  <Panel
    as="section"
    class="column"
    :class="`column--${model.dropState}`"
    :label="model.title"
    :count="model.count"
    :total="model.total"
    :label-id="headingId"
    :padded="false"
    :data-drop-column="model.column"
    :aria-disabled="refused ? 'true' : undefined"
  >
    <div class="column__body">
      <p v-if="refused && model.reason" class="column__reason" data-reason>{{ model.reason }}</p>
      <p v-else-if="model.action" class="column__reason column__reason--action" data-drop-action>{{ model.action }}</p>
      <div class="column__cards">
        <template v-if="grouped">
          <h3 class="column__group">Stories <span class="column__group-count">{{ groups.stories.length }}</span></h3>
          <ul role="list" class="column__list">
            <TicketCard
              v-for="c in groups.stories"
              :key="c.ticket.number"
              :card="c"
              :dragging="c.ticket.number === draggingTicket"
              :drop-mark="markOf(c)"
              @open="emit('open', $event)"
              @filter-story="emit('filter-story', $event)"
            />
          </ul>
          <h3 class="column__group">Tasks <span class="column__group-count">{{ groups.tasks.length }}</span></h3>
          <ul role="list" class="column__list">
            <TicketCard
              v-for="c in groups.tasks"
              :key="c.ticket.number"
              :card="c"
              :dragging="c.ticket.number === draggingTicket"
              :drop-mark="markOf(c)"
              @open="emit('open', $event)"
              @filter-story="emit('filter-story', $event)"
            />
          </ul>
        </template>
        <ul v-else-if="model.cards.length > 0" role="list" class="column__list">
          <TicketCard
            v-for="c in model.cards"
            :key="c.ticket.number"
            :card="c"
            :dragging="c.ticket.number === draggingTicket"
            :drop-mark="markOf(c)"
            @open="emit('open', $event)"
            @filter-story="emit('filter-story', $event)"
          />
        </ul>
        <p v-else class="column__empty">No tickets</p>
        <UiButton v-if="model.column === 'planning'" class="column__add" size="sm" variant="ghost" data-add-ticket @click="create.open()">
          <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M6 1v10M1 6h10" />
          </svg>
          Add to Planning
        </UiButton>
        <UiButton v-if="model.more" class="column__more" size="sm" data-load-more @click="emit('load-more')">
          Load older
        </UiButton>
      </div>
    </div>
  </Panel>
</template>

<style scoped>
.column {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
/* Column names are the first thing to find on the board: full contrast and a size up. */
.column :deep(.panel__label) {
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.column__body {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  /* Room for the focus ring of the cards inside the scrolling area. */
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 2) calc(var(--omo-space) * 2);
}
.column__cards {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  align-content: start;
}
.column__list {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.column__group {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin: calc(var(--omo-space) * 1) 0 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
}
.column__group:first-child {
  margin-top: 0;
}
.column__group::after {
  content: '';
  flex: 1 1 auto;
  border-top: var(--omo-border) solid var(--omo-line);
}
.column__group-count {
  color: var(--omo-fg);
}
.column__empty {
  margin: 0;
  padding: calc(var(--omo-space) * 3) 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: center;
}
.column__more,
.column__add {
  justify-self: center;
}

/* Drag states. */
.column--allowed {
  border-style: dashed;
  border-color: var(--omo-accent);
}
.column--over {
  border-color: var(--omo-accent);
  outline: var(--omo-border) solid var(--omo-accent);
  outline-offset: calc(var(--omo-border) * -2);
  background: var(--omo-accent-soft);
}
.column--over :deep(.panel__head) {
  --panel-cut: linear-gradient(
    to bottom,
    transparent calc(50% - var(--omo-border)),
    var(--omo-accent-soft) calc(50% - var(--omo-border))
  );
}
.column--refused::after {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: repeating-linear-gradient(
    -45deg,
    var(--omo-line) 0 var(--omo-border),
    transparent var(--omo-border) calc(var(--omo-space) * 2)
  );
  opacity: 0.55;
}
.column--refused :deep(.panel__head),
.column--refused .column__cards {
  opacity: 0.55;
}
.column__reason {
  position: sticky;
  z-index: 3;
  top: 0;
  margin: 0 0 calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid var(--omo-line-strong);
  background: var(--omo-surface);
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.column__reason--action {
  border-color: var(--omo-accent);
}
</style>

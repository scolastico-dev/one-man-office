<script setup lang="ts">
// The "Move…" menu of a card: every column, with what a move there does or
// why it is not possible, and the top or bottom of the card's own group. It
// moves cards for those who cannot or will not drag them.
import { computed, ref } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import type { Target } from '../../board/moveRules'
import { beforeFor } from '../../board/ordering'
import UiMenu, { type MenuItem } from '../../components/ui/UiMenu.vue'
import { COLUMN_TITLES } from './model'
import { placesCard, type MoveOptions } from './useMoves'

const props = defineProps<{
  ticket: TicketSummary
  /** Where the card may go and the cards of each column; asked only once the menu is about to be used. */
  moves: Pick<MoveOptions, 'targets' | 'order'>
}>()
const emit = defineEmits<{ move: [target: Target, before: number | null] }>()

/**
 * The items are worked out only once the menu is pointed at or focused, so a
 * card does not draw itself again for every change on the board.
 */
const armed = ref(false)

function hint(t: Target): string {
  switch (t.action) {
    case 'approve':
      return t.resultColumn === 'review' ? 'Approves the review.' : 'Approves.'
    case 'withdraw':
      return 'Withdraws the approval.'
    case 'stop':
      return 'Stops the agent. You confirm first.'
    case 'reject':
      return 'Rejects. You write a comment first.'
    default:
      return 'Changes the priority.'
  }
}

const plan = computed(() => {
  if (!armed.value) return null
  const t = props.ticket
  const targets = props.moves.targets(t.number)
  const own = props.moves.order(t.column).filter((x) => x.type === t.type)
  const index = Math.max(0, own.findIndex((x) => x.number === t.number))
  return { t, targets, index, last: own.length - 1 }
})

const items = computed((): MenuItem[] => {
  const p = plan.value
  if (p === null) return []
  const { t, targets } = p
  const out: MenuItem[] = COLUMNS.map((c) => {
    const target = targets[c]
    if (c === t.column) {
      return { id: c, label: COLUMN_TITLES[c], disabled: true, hint: target.allowed ? `#${t.number} is in this column.` : target.reason }
    }
    return target.allowed ? { id: c, label: COLUMN_TITLES[c], hint: hint(target) } : { id: c, label: COLUMN_TITLES[c], disabled: true, hint: target.reason }
  })
  const own = targets[t.column]
  if (own.allowed && own.action === 'reorder') {
    const kind = t.type === 'story' ? 'stories' : 'tasks'
    const where = COLUMN_TITLES[t.column]
    out.push(
      p.index === 0
        ? { id: 'top', label: 'Move to top', disabled: true, hint: 'It is at the top already.' }
        : { id: 'top', label: 'Move to top', hint: `Top of the ${kind} in ${where}.` },
      p.index === p.last
        ? { id: 'bottom', label: 'Move to bottom', disabled: true, hint: 'It is at the bottom already.' }
        : { id: 'bottom', label: 'Move to bottom', hint: `Bottom of the ${kind} in ${where}.` },
    )
  }
  return out
})

function onSelect(id: string) {
  const p = plan.value
  if (p === null) return
  const { t, targets } = p
  if (id === 'top' || id === 'bottom') {
    const order = props.moves.order(t.column)
    const others = order.filter((x) => x.type === t.type && x.number !== t.number).length
    emit('move', targets[t.column], beforeFor(order, t, id === 'top' ? 0 : others))
    return
  }
  const c = id as Column
  const target = targets[c]
  if (!target.allowed) return
  // Another column: the end of the card's group there, as a drop on the column's tab.
  const order = props.moves.order(c)
  const before = placesCard(target) ? beforeFor(order, t, order.filter((x) => x.type === t.type && x.number !== t.number).length) : null
  emit('move', target, before)
}
</script>

<template>
  <span class="move-menu" data-no-drag data-move-menu @pointerenter="armed = true" @pointerdown.capture="armed = true" @focusin="armed = true">
    <UiMenu :label="`Move #${ticket.number}…`" :items="items" icon-only size="sm" variant="ghost" align="end" @select="onSelect">
      <template #icon>
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
          <path d="M1 4h9M7.5 1.5 10 4 7.5 6.5M11 8H2M4.5 5.5 2 8l2.5 2.5" />
        </svg>
      </template>
    </UiMenu>
  </span>
</template>

<style scoped>
.move-menu {
  display: inline-flex;
}
</style>

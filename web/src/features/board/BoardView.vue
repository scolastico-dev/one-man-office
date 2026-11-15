<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, onUpdated, ref, watch } from 'vue'
import type { Column } from '../../api/types'
import { COLUMNS } from '../../api/types'
import BoardColumn from './BoardColumn.vue'
import ColumnSwitcher from './ColumnSwitcher.vue'
import DragGhost from './DragGhost.vue'
import type { CardModel, ColumnModel } from './model'
import type { Drag } from './useDrag'
import type { KeyboardMove } from './useKeyboardMove'

const props = withDefaults(
  defineProps<{
    columns: ColumnModel[]
    /** Number of the card being dragged, shown as its placeholder. */
    draggingTicket?: number | null
    /** Column shown first on narrow screens. */
    initialColumn?: Column
    /** Cards can be dragged with this drag; without it they cannot. */
    drag?: Drag | null
    /** Cards can be moved with the keyboard through this; without it they cannot. */
    keyboard?: KeyboardMove | null
  }>(),
  { draggingTicket: null, initialColumn: 'planning', drag: null, keyboard: null },
)
const emit = defineEmits<{ open: [n: number]; 'filter-story': [n: number]; 'load-more': [] }>()

/** Below this width the board shows one column at a time. */
const NARROW = '(max-width: 767.98px)'

/** The one column shown below 768 px. */
const active = ref<Column>(props.initialColumn)
const root = ref<HTMLElement>()

const dragged = computed(() => props.drag?.active.value?.n ?? props.keyboard?.picked.value?.n ?? props.draggingTicket)

/** The card that follows the pointer, as the board shows it now. */
const ghostCard = computed((): { card: CardModel; width: number } | null => {
  const g = props.drag?.ghost.value
  if (!g) return null
  for (const c of props.columns) {
    const card = c.cards.find((x) => x.ticket.number === g.n)
    if (card) return { card, width: g.width }
  }
  return null
})

function cardOf(target: EventTarget | null): { li: HTMLElement; n: number } | null {
  if (!(target instanceof Element)) return null
  const li = target.closest<HTMLElement>('li[data-ticket]')
  const n = Number(li?.dataset.ticket)
  return li && Number.isInteger(n) ? { li, n } : null
}

function onPointerDown(e: PointerEvent) {
  if (!props.drag) return
  const c = cardOf(e.target)
  if (!c) return
  props.keyboard?.cancel('pointer')
  props.drag.onPointerDown(e, c.n)
}

function onKeydown(e: KeyboardEvent) {
  if (!props.keyboard || props.drag?.active.value) return
  // Only the card itself (its title link); the controls on it keep their keys.
  if (!(e.target instanceof Element) || !e.target.matches('[data-card-link]')) return
  const c = cardOf(e.target)
  if (c) props.keyboard.onKeydown(e, c.n)
}

function onFocusOut(e: FocusEvent) {
  const c = cardOf(e.target)
  if (c) props.keyboard?.onFocusOut(e, c.n)
}

// ---- keeping the user's place ----------------------------------------------
// A card is drawn anew when it changes column or group, which takes the focus
// away from it. The board remembers the card that had the focus and gives it
// back; when the card is no longer shown (a filter hides it, it went to a
// column not shown, or out of the loaded part of Done), the card now at its
// place in its column gets the focus, else the shown column's tab or the board.

let place: { n: number; column: Column | null; index: number } | null = null

function shown(el: Element): boolean {
  const check = (el as Element & { checkVisibility?: () => boolean }).checkVisibility
  return typeof check === 'function' ? check.call(el) : true
}

function shownCards(section: Element | null): HTMLElement[] {
  return section ? [...section.querySelectorAll<HTMLElement>('li[data-ticket]')].filter(shown) : []
}

function remember(li: HTMLElement, n: number) {
  const section = li.closest<HTMLElement>('section[data-drop-column]')
  const column = (section?.dataset.dropColumn ?? null) as Column | null
  place = { n, column, index: Math.max(0, shownCards(section).indexOf(li)) }
}

function onDocFocusIn(e: FocusEvent) {
  const c = root.value?.contains(e.target as Node) ? cardOf(e.target) : null
  if (c) remember(c.li, c.n)
  // Focus in a dialog (a move's confirmation) comes back to the card afterwards.
  else if (!(e.target instanceof Element && e.target.closest('dialog'))) place = null
}

function onDocPointerDown(e: PointerEvent) {
  if (!(e.target instanceof Element)) return
  if (!cardOf(e.target) && !e.target.closest('dialog')) place = null
}

function focusCard(n: number): boolean {
  const link = root.value?.querySelector<HTMLElement>(`li[data-ticket="${n}"] [data-card-link]`)
  if (!link || !shown(link)) return false
  link.focus()
  return true
}

/** Gives the focus back to the remembered card, or to its place, when it was lost. */
function restoreFocus() {
  const p = place
  const el = root.value
  if (p === null || !el) return
  const now = document.activeElement
  if (now && now !== document.body && now !== document.documentElement && now.isConnected && shown(now)) return
  if (focusCard(p.n)) return
  const cards = shownCards(p.column ? el.querySelector(`section[data-drop-column="${p.column}"]`) : null)
  const neighbour = cards[Math.min(p.index, cards.length - 1)]
  const n = Number(neighbour?.dataset.ticket)
  if (Number.isInteger(n) && focusCard(n)) return
  const tab = el.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')
  if (tab && shown(tab)) tab.focus()
  else el.querySelector<HTMLElement>('[data-board]')?.focus()
}

onUpdated(restoreFocus)

let media: MediaQueryList | null = null

function columnInView(): Column | null {
  const board = root.value?.querySelector<HTMLElement>('[data-board]')
  if (!board) return null
  for (const s of board.querySelectorAll<HTMLElement>('section[data-drop-column]')) {
    if (s.offsetLeft + s.offsetWidth / 2 >= board.scrollLeft) return s.dataset.dropColumn as Column
  }
  return null
}

/** Crossing 768 px keeps the user's place: the column of the focused card, or the one in view. */
function onBreakpoint(e: MediaQueryListEvent) {
  if (e.matches) {
    const c = place?.column ?? columnInView()
    if (c && (COLUMNS as readonly string[]).includes(c)) active.value = c
  }
  void nextTick(() => {
    if (!e.matches) {
      const board = root.value?.querySelector<HTMLElement>('[data-board]')
      const section = board?.querySelector<HTMLElement>(`section[data-drop-column="${active.value}"]`)
      if (board && section) board.scrollLeft = section.offsetLeft
    }
    restoreFocus()
  })
}

/**
 * Where the empty board's box (the first steps) starts, in pixels from the
 * top of the view: below the Planning column's "Add to Planning", which it
 * would cover otherwise wherever the box is wider than the room beside that
 * column. Null while the button is not shown (the CSS place applies).
 */
const emptyTop = ref<number | null>(null)
/** The room between the button and the box's place (whose padding holds the box's label, on its top border). */
const EMPTY_GAP_SPACES = 2
function placeEmpty() {
  const view = root.value
  const add = view?.querySelector<HTMLElement>('[data-add-ticket]')
  if (!view || !add || add.getClientRects().length === 0) {
    emptyTop.value = null
    return
  }
  const space = parseFloat(getComputedStyle(view).getPropertyValue('--omo-space')) || 4
  emptyTop.value = Math.ceil(add.getBoundingClientRect().bottom - view.getBoundingClientRect().top + EMPTY_GAP_SPACES * space)
}
let emptyObserver: ResizeObserver | null = null
watch([active, () => props.columns], placeEmpty, { flush: 'post' })

onMounted(() => {
  document.addEventListener('focusin', onDocFocusIn)
  document.addEventListener('pointerdown', onDocPointerDown, true)
  if (typeof matchMedia === 'function') {
    media = matchMedia(NARROW)
    media.addEventListener('change', onBreakpoint)
  }
  placeEmpty()
  if (typeof ResizeObserver === 'function' && root.value) {
    emptyObserver = new ResizeObserver(placeEmpty)
    emptyObserver.observe(root.value)
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('focusin', onDocFocusIn)
  document.removeEventListener('pointerdown', onDocPointerDown, true)
  media?.removeEventListener('change', onBreakpoint)
  emptyObserver?.disconnect()
})

defineExpose({
  /** The column shown on a narrow screen; null when every column is shown. */
  shownColumn(): Column | null {
    return typeof matchMedia === 'function' && matchMedia(NARROW).matches ? active.value : null
  },
  /** Shows a column on a narrow screen and puts the focus on card `n` in it. */
  showColumn(c: Column, n?: number) {
    active.value = c
    void nextTick(() => {
      if (n !== undefined && focusCard(n)) return
      root.value?.querySelector<HTMLElement>(`[role="tab"][data-drop-column="${c}"]`)?.focus()
    })
  },
  restoreFocus,
})
</script>

<template>
  <div ref="root" class="board-view" @pointerdown="onPointerDown" @keydown="onKeydown" @focusout="onFocusOut">
    <ColumnSwitcher class="board-view__switcher" :columns="columns" :active="active" @select="active = $event" />
    <div class="board" data-board role="region" aria-label="Board columns" tabindex="0">
      <BoardColumn
        v-for="c in columns"
        :key="c.column"
        :class="{ 'column--active': c.column === active }"
        :model="c"
        :dragging-ticket="dragged"
        @open="emit('open', $event)"
        @filter-story="emit('filter-story', $event)"
        @load-more="emit('load-more')"
      />
    </div>
    <div v-if="$slots.empty" class="board__empty" :style="emptyTop === null ? undefined : { top: `${emptyTop}px` }"><slot name="empty" /></div>
    <DragGhost v-if="drag && ghostCard" :card="ghostCard.card" :width="ghostCard.width" :place="drag.setGhost" />
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
/*
 * Below the columns' "No tickets" and "Add to Planning" (the script sets the
 * top from the button), so the empty columns still read as columns; it
 * scrolls when the window is too short for the whole box (room is kept for
 * the box's shadow).
 */
.board__empty {
  position: absolute;
  z-index: 4;
  inset: calc(var(--omo-space) * 21) calc(var(--omo-space) * 3) 0;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3) 0;
  overflow-y: auto;
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
    inset: calc(var(--omo-space) * 33) calc(var(--omo-space) * 2) 0;
  }
}
</style>

<script setup lang="ts">
// The board page: top bar, the five columns from the store, the first steps
// on an empty board, the ticket's place at /<n>, the create dialog, and
// moving cards by dragging them, with the keyboard or through their menu.
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from '../../components/ui/useToast'
import AppShell from '../../layout/AppShell.vue'
import { useBoard } from '../../store'
import CreateTicketDialog from '../create/CreateTicketDialog.vue'
import { useCreateTicket } from '../create/useCreateTicket'
import NothingMatches from '../filters/NothingMatches.vue'
import { useFilters } from '../filters/useFilters'
import FirstSteps from '../onboarding/FirstSteps.vue'
import SidebarRoot from '../sidebar/SidebarRoot.vue'
import TicketPanel from '../ticket/TicketPanel.vue'
import TopBar from '../topbar/TopBar.vue'
import BoardView from './BoardView.vue'
import RejectDialog from './dialogs/RejectDialog.vue'
import StopDialog from './dialogs/StopDialog.vue'
import { COLUMN_TITLES } from './model'
import { useBoardColumns } from './useBoardColumns'
import { useDrag } from './useDrag'
import { useKeyboardMove } from './useKeyboardMove'
import { createMoves, provideMoves } from './useMoves'

const store = useBoard()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const filterState = useFilters()
const moves = createMoves({ store })
provideMoves(moves)
const drag = useDrag({
  targets: moves.targets,
  order: moves.order,
  unanswered: moves.unanswered,
  drop: moves.drop,
  pickedUp: moves.pickedUp,
  cancelled: moves.cancelled,
})
// The dragged card moved or vanished under the pointer: the drag ends, and the user is told.
moves.follow(drag.active, () => drag.cancel('gone'))
// Cards arrived or left under a still pointer: find the drop place again.
watch(
  () => store.columns.value,
  () => {
    if (drag.active.value !== null) void nextTick(drag.refresh)
  },
)
const keyboard = useKeyboardMove({ targets: moves.targets, order: moves.order, unanswered: moves.unanswered, drop: moves.drop, cancelled: moves.cancelled })
moves.follow(keyboard.picked, () => keyboard.cancel('gone'))
const moving = computed(() => drag.active.value ?? keyboard.active.value)
const columns = useBoardColumns({ store, filters: filterState, drag: { active: moving, targets: moves.targets }, moves })
const view = ref<InstanceType<typeof BoardView>>()

// A move's dialog closed: the focus goes back to the card, wherever it is now.
watch(
  () => moves.dialog.value,
  (d) => {
    if (d === null) void nextTick(() => view.value?.restoreFocus())
  },
)

// On a narrow screen a card moved to another column leaves the screen: a notice offers to show that column.
const shownMoves = new Map<number, { requestId: number; column: string; toast: number }>()
watch(
  () => [...store.pending].map(([n, p]) => ({ n, requestId: p.requestId, column: p.column })),
  (now) => {
    for (const [n, m] of shownMoves) {
      if (now.some((p) => p.n === n && p.requestId === m.requestId)) continue
      shownMoves.delete(n)
      // Refused: the card is back where it was, and the notice would be wrong.
      if (store.view(n)?.column !== m.column) toast.dismiss(m.toast)
    }
    for (const p of now) {
      if (shownMoves.get(p.n)?.requestId === p.requestId) continue
      const shown = view.value?.shownColumn() ?? null
      if (shown === null || shown === p.column) continue
      const title = COLUMN_TITLES[p.column]
      const id = toast.show({ title: `#${p.n} is in ${title} now`, action: { label: `Show ${title}`, run: () => view.value?.showColumn(p.column, p.n) } })
      shownMoves.set(p.n, { requestId: p.requestId, column: p.column, toast: id })
    }
  },
)
const create = useCreateTicket()

/** No ticket at all, open or Done: the first start. */
const firstStart = computed(() => store.state.loaded && store.state.tickets.size === 0)

/** Filters hide every ticket of a board that has some. */
const nothingMatches = computed(
  () => !firstStart.value && store.state.loaded && store.state.tickets.size > 0 && filterState.activeCount.value > 0 && columns.value.every((c) => c.count === 0),
)

const ticketNumber = computed(() => (route.name === 'ticket' ? Number(route.params.n) : null))

function openTicket(n: number) {
  void router.push({ path: `/${n}`, query: route.query })
}

/** Back to the board: the browser's back when the board is where the user came from, else in place. */
function closeTicket() {
  const back = router.options.history.state.back
  if (typeof back === 'string' && router.resolve(back).name === 'board') router.back()
  else void router.replace({ path: '/', query: route.query })
}

function filterStory(n: number) {
  filterState.filters.value = { ...filterState.filters.value, story: n }
}

function loadMore() {
  store.loadOlderDone().catch((e: unknown) => {
    toast.show({ tone: 'error', title: 'Could not load older tickets', message: e instanceof Error ? e.message : undefined })
  })
}
</script>

<template>
  <AppShell>
    <template #sidebar><SidebarRoot /></template>
    <template #topbar><TopBar /></template>
    <h1 class="sr-only">Board</h1>
    <BoardView ref="view" :columns="columns" :drag="drag" :keyboard="keyboard" @open="openTicket" @filter-story="filterStory" @load-more="loadMore">
      <template v-if="firstStart" #empty>
        <FirstSteps />
      </template>
      <template v-else-if="nothingMatches" #empty>
        <NothingMatches />
      </template>
    </BoardView>
  </AppShell>
  <TicketPanel v-if="ticketNumber !== null" :key="ticketNumber" :number="ticketNumber" @close="closeTicket" />
  <RejectDialog
    v-if="moves.dialog.value?.kind === 'reject'"
    open
    :ticket="moves.dialog.value.ticket"
    :busy="moves.dialogBusy.value"
    :error="moves.dialogError.value"
    @close="moves.closeDialog()"
    @confirm="moves.confirmDialog($event)"
  />
  <StopDialog
    v-if="moves.dialog.value?.kind === 'stop'"
    open
    :ticket="moves.dialog.value.ticket"
    :to="moves.dialog.value.to"
    :agents="moves.dialog.value.agents"
    :busy="moves.dialogBusy.value"
    :error="moves.dialogError.value"
    @close="moves.closeDialog()"
    @confirm="moves.confirmDialog()"
  />
  <CreateTicketDialog :open="create.state.open" :initial-story="create.state.initialStory" @close="create.close()" />
</template>

<script setup lang="ts">
// The board page: top bar, the five columns from the store, the first steps
// on an empty board, the ticket's place at /<n> and the create dialog.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from '../../components/ui/useToast'
import AppShell from '../../layout/AppShell.vue'
import { useBoard } from '../../store'
import CreateTicketDialog from '../create/CreateTicketDialog.vue'
import { useCreateTicket } from '../create/useCreateTicket'
import NothingMatches from '../filters/NothingMatches.vue'
import { useFilters } from '../filters/useFilters'
import FirstSteps from '../onboarding/FirstSteps.vue'
import TicketPanel from '../ticket/TicketPanel.vue'
import TopBar from '../topbar/TopBar.vue'
import BoardView from './BoardView.vue'
import { useBoardColumns } from './useBoardColumns'

const store = useBoard()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const filterState = useFilters()
const columns = useBoardColumns({ store, filters: filterState })
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
    <template #topbar><TopBar /></template>
    <h1 class="sr-only">Board</h1>
    <BoardView :columns="columns" @open="openTicket" @filter-story="filterStory" @load-more="loadMore">
      <template v-if="firstStart" #empty>
        <FirstSteps />
      </template>
      <template v-else-if="nothingMatches" #empty>
        <NothingMatches />
      </template>
    </BoardView>
  </AppShell>
  <TicketPanel v-if="ticketNumber !== null" :key="ticketNumber" :number="ticketNumber" @close="closeTicket" />
  <CreateTicketDialog :open="create.state.open" :initial-story="create.state.initialStory" @close="create.close()" />
</template>

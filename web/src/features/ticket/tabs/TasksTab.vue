<script setup lang="ts">
// The Tasks tab of a story: its open tasks from the board and its Done tasks,
// read here and kept only here (the board store never gets them). Each row
// links to the task and says where it is.
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { listTickets } from '../../../api/endpoints'
import type { TicketSummary } from '../../../api/types'
import { badgeFor } from '../../../board/badges'
import Chip from '../../../components/ui/Chip.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import TypeIcon from '../../../components/ui/TypeIcon.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { COLUMN_TITLES } from '../../board/model'
import { useCreateTicket } from '../../create/useCreateTicket'
import TicketLink from '../TicketLink.vue'

/** How long a read waits after the last change that asked for it. */
const REREAD_DELAY_MS = 200
const DONE_LIMIT = 500

const props = defineProps<{ ticket: TicketSummary }>()

const session = useSession()
const store = useBoard()
const create = useCreateTicket()

const done = shallowRef<TicketSummary[]>([])
const error = ref('')

/**
 * Open tasks first, then Done ones by number. A task the board has just seen
 * reach Done is shown as Done right away, before the Done list is read again;
 * one that left Done shows as open.
 */
const tasks = computed(() => {
  const live = store.tasksOf(props.ticket.number)
  const open = live.filter((t) => t.column !== 'done')
  const finished = new Map(done.value.map((t) => [t.number, t]))
  for (const t of live) if (t.column === 'done') finished.set(t.number, t)
  for (const t of open) finished.delete(t.number)
  return [...open, ...[...finished.values()].sort((a, b) => a.number - b.number)]
})

let attempt = 0
async function read(): Promise<void> {
  const mine = ++attempt
  try {
    const { data } = await listTickets(session.client, { story: props.ticket.number, done_limit: DONE_LIMIT })
    if (mine !== attempt) return
    done.value = data.tickets.filter((t) => t.story === props.ticket.number && t.column === 'done').sort((a, b) => a.number - b.number)
    error.value = ''
  } catch (e) {
    if (mine !== attempt) return
    error.value = e instanceof Error ? e.message : 'Could not read the tasks.'
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
function later(): void {
  clearTimeout(timer)
  timer = setTimeout(() => void read(), REREAD_DELAY_MS)
}

void read()
const offSnapshot = store.onSnapshot(later)
// A task that reaches Done leaves the open set: the Done list is read again.
const stopWatching = watch(
  () => store.tasksOf(props.ticket.number).map((t) => `${t.number}:${t.column}`).join(','),
  (now, before) => {
    if (now !== before) later()
  },
)
onBeforeUnmount(() => {
  clearTimeout(timer)
  attempt++
  stopWatching()
  offSnapshot()
})
</script>

<template>
  <div class="ttab" data-ticket-tasks>
    <h3 class="sr-only">Tasks</h3>
    <div class="ttab__bar">
      <UiButton size="sm" data-add-task @click="create.open(ticket.number)">Add task</UiButton>
    </div>
    <p v-if="error" class="ttab__error" role="alert" data-tasks-error>{{ error }}</p>
    <ul v-if="tasks.length > 0" class="ttab__list" aria-label="Tasks of this story">
      <li v-for="t in tasks" :key="t.number" class="ttab__row" :data-task="t.number">
        <span class="ttab__main">
          <TypeIcon type="task" />
          <TicketLink class="ttab__link" :number="t.number">#{{ t.number }}</TicketLink>
          <span class="ttab__title">{{ visibleText(t.title) }}</span>
        </span>
        <span class="ttab__meta">
          <span class="ttab__column" data-task-column>{{ COLUMN_TITLES[t.column] }}</span>
          <StatusBadge v-if="badgeFor(t, store.slots.value)" :tone="badgeFor(t, store.slots.value)!.tone" :text="badgeFor(t, store.slots.value)!.text" :title="badgeFor(t, store.slots.value)!.title" />
          <Chip v-for="d in t.depends_on" :key="d" :label="`depends on #${d}`" />
        </span>
      </li>
    </ul>
    <p v-else class="ttab__muted" data-tasks-empty>This story has no tasks yet.</p>
  </div>
</template>

<style scoped>
.ttab {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.ttab__bar {
  display: flex;
  justify-content: flex-end;
}
.ttab__list {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.ttab__row {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.ttab__main,
.ttab__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.ttab__link {
  color: var(--omo-accent);
}
.ttab__title {
  min-width: 0;
  overflow-wrap: anywhere;
}
.ttab__column {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.ttab__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.ttab__error {
  margin: 0;
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>

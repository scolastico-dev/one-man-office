<script setup lang="ts">
// The Agents tab: every session that has worked on the ticket, newest first.
// A running agent opens its terminal in place (one at a time); an ended one
// shows its saved log. The list is read again shortly after an agent event of
// this ticket, after a resync, and when the stream is live again.
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { agentTerminalPath, getTicketAgents } from '../../../api/endpoints'
import type { AgentSession, ApiEvent, TicketSummary } from '../../../api/types'
import { timeKey } from '../../../api/time'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { ROLE_LABELS } from '../../board/model'
import AgentRow from '../../sidebar/AgentRow.vue'
import LogView from '../../terminal/LogView.vue'
import { LazyTerminal } from '../../terminal/LazyTerminal'
import { useTicketLayout } from '../ticketLayout'

/** How long a read waits after the last event that asked for it. */
const REREAD_DELAY_MS = 200

const props = defineProps<{ ticket: TicketSummary }>()

const session = useSession()
const store = useBoard()
// On the ticket's page the open terminal takes the height the tab has left; in the panel it keeps its own.
const onPage = useTicketLayout() === 'page'

const sessions = shallowRef<AgentSession[]>([])
const loaded = ref(false)
const error = ref('')
const openId = ref<string | null>(null)

const newestFirst = (a: AgentSession, b: AgentSession) => {
  const ka = timeKey(a.started_at)
  const kb = timeKey(b.started_at)
  return ka < kb ? 1 : ka > kb ? -1 : a.id < b.id ? -1 : 1
}

let attempt = 0
async function read(): Promise<void> {
  const mine = ++attempt
  try {
    const { data } = await getTicketAgents(session.client, props.ticket.number)
    if (mine !== attempt) return
    sessions.value = [...data].sort(newestFirst)
    error.value = ''
    loaded.value = true
  } catch (e) {
    if (mine !== attempt) return
    error.value = e instanceof Error ? e.message : 'Could not read the agents.'
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
function later(): void {
  clearTimeout(timer)
  timer = setTimeout(() => void read(), REREAD_DELAY_MS)
}

void read()
const off = [
  ...['agent.created', 'agent.updated', 'agent.ended'].map((kind) =>
    store.onEvent(kind, (ev: ApiEvent) => {
      if (ev.ticket === props.ticket.number) later()
    }),
  ),
  store.onSnapshot(later),
]
const stopWatching = watch(
  () => store.connection.value,
  (now, before) => {
    if (now === 'live' && before !== 'live') later()
  },
)
onBeforeUnmount(() => {
  clearTimeout(timer)
  attempt++
  stopWatching()
  for (const f of off) f()
})

const isRunning = (s: AgentSession) => s.state !== 'ended'

const label = (s: AgentSession) => `Terminal of ${ROLE_LABELS[s.role] ?? s.role} ${visibleText(s.name)} on #${props.ticket.number}`
const open = computed(() => sessions.value.find((s) => s.id === openId.value) ?? null)

function toggle(s: AgentSession): void {
  openId.value = openId.value === s.id ? null : s.id
}
</script>

<template>
  <div class="atab" :class="{ 'atab--page': onPage }" data-ticket-agents>
    <h3 class="sr-only">Agents</h3>
    <p v-if="error" class="atab__error" role="alert" data-agents-error>{{ error }}</p>
    <ul v-if="sessions.length > 0" class="atab__list" aria-label="Agents of this ticket">
      <li v-for="s in sessions" :key="s.id" class="atab__item" :class="{ 'atab__item--open': onPage && open?.id === s.id }" :data-session="s.id">
        <AgentRow :session="s" compact :aria-expanded="openId === s.id" @open="toggle" />
        <div v-if="!isRunning(s)" class="atab__ended">
          <span class="atab__muted" data-ended>Ended<template v-if="s.end_reason"> ({{ visibleText(s.end_reason) }})</template></span>
          <UiButton size="sm" data-show-log :aria-expanded="openId === s.id" @click="toggle(s)">{{ openId === s.id ? 'Hide log' : 'Show log' }}</UiButton>
        </div>
        <div v-if="open && open.id === s.id" class="atab__detail">
          <LogView v-if="!isRunning(s)" :session-id="s.id" />
          <template v-else-if="s.terminal_id">
            <p class="atab__muted">Typing here types into the agent.</p>
            <LazyTerminal :key="s.terminal_id" :src="agentTerminalPath(s.terminal_id)" :label="label(s)" ended-text="This agent ended." :fill="onPage" />
          </template>
          <p v-else class="atab__muted" role="status">The terminal is not ready yet.</p>
        </div>
      </li>
    </ul>
    <p v-else-if="loaded" class="atab__muted" data-agents-empty>No agent has worked on #{{ ticket.number }} yet.</p>
  </div>
</template>

<style scoped>
.atab {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
/* On the page: the list, the open agent and its terminal pass the height down; the terminal never gets less than a few rows. */
.atab.atab--page {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
}
.atab--page .atab__list {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
}
.atab__item--open {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 18rem;
}
.atab__item--open > .atab__detail {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
}
.atab__list {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.atab__item {
  min-width: 0;
}
.atab__ended {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  padding: var(--omo-space) calc(var(--omo-space) * 2.5);
}
.atab__detail {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.atab__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.atab__error {
  margin: 0;
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>

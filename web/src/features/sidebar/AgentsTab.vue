<script setup lang="ts">
// The Agents tab: the scheduler's load, and every live agent as a row. A row
// opens that agent's terminal inside the tab, with a way back to the list and
// a link to its ticket. An agent that ends while it is open says so.
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { RouterLink } from 'vue-router'
import { agentTerminalPath } from '../../api/endpoints'
import type { AgentSession } from '../../api/types'
import LiveDot from '../../components/ui/LiveDot.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { ROLE_LABELS } from '../board/model'
import AgentRow from './AgentRow.vue'
import { stateWord, useAgents } from './useAgents'
import { useSidebar } from './useSidebar'

// The terminal is loaded on first use; without the terminal feature the detail
// shows everything but the terminal itself.
const found = import.meta.glob<{ LazyTerminal: Component }>('../terminal/LazyTerminal.ts', { eager: true })
const Terminal: Component | null = Object.values(found)[0]?.LazyTerminal ?? null

const agents = useAgents()
const sidebar = useSidebar()

const sessions = agents.sessions
const scheduler = agents.scheduler
const selected = computed(() => sidebar.selection.value)

/** The open agent as last seen, so its detail can say it ended after it left the list. */
const lastSeen = ref<AgentSession | null>(null)
const open = computed(() => (selected.value === null ? null : (sessions.value.find((s) => s.id === selected.value) ?? null)))
watch(
  open,
  (s) => {
    if (s) lastSeen.value = s
  },
  { immediate: true },
)
watch(selected, (id) => {
  if (id === null) lastSeen.value = null
  else if (lastSeen.value?.id !== id) lastSeen.value = open.value
})
const ended = computed(() => selected.value !== null && open.value === null)
const shown = computed(() => open.value ?? lastSeen.value)

const list = ref<HTMLElement>()
const back = ref<InstanceType<typeof UiButton>>()
const comesFrom = ref<string | null>(null)

function show(s: AgentSession) {
  comesFrom.value = s.id
  sidebar.select(s.id)
  void nextTick(() => (back.value?.$el as HTMLElement | undefined)?.focus())
}

function close() {
  sidebar.select(null)
  const id = comesFrom.value
  void nextTick(() => {
    const row = id === null ? null : list.value?.querySelector<HTMLElement>(`[data-session="${CSS.escape(id)}"]`)
    ;(row ?? list.value?.querySelector<HTMLElement>('button'))?.focus()
  })
}

const label = computed(() => {
  const s = shown.value
  return s ? `Terminal of ${ROLE_LABELS[s.role] ?? s.role} ${visibleText(s.name)}${s.ticket === null ? '' : ` on #${s.ticket}`}` : 'Terminal'
})
</script>

<template>
  <div class="agents">
    <template v-if="selected !== null">
      <div class="agents__bar">
        <UiButton ref="back" size="sm" data-agent-back @click="close">Back</UiButton>
        <RouterLink v-if="shown && shown.ticket !== null" class="agents__link" :to="`/${shown.ticket}`" data-agent-ticket>Open #{{ shown.ticket }}</RouterLink>
      </div>
      <p v-if="shown" class="agents__who">
        <LiveDot v-if="!ended" :state="shown.state as 'running' | 'starting' | 'waiting'" />
        <span>{{ ROLE_LABELS[shown.role] }} · {{ visibleText(shown.name) }}</span>
        <span v-if="!ended && stateWord(shown.state)" class="agents__muted">{{ stateWord(shown.state) }}</span>
      </p>
      <p v-if="ended" class="agents__ended" role="status" data-agent-ended>This agent ended.</p>
      <template v-else-if="shown">
        <p class="agents__note">Typing here types into the agent.</p>
        <component :is="Terminal" v-if="Terminal && shown.terminal_id" :key="shown.terminal_id" :src="agentTerminalPath(shown.terminal_id)" :label="label" :ended-text="'This agent ended.'" />
        <p v-else-if="!shown.terminal_id" class="agents__muted" role="status">The terminal is not ready yet.</p>
      </template>
    </template>

    <template v-else>
      <div class="agents__head">
        <p v-if="scheduler" class="agents__slots" data-slots>
          Slots: <span class="agents__count">{{ scheduler.running }} of {{ scheduler.limit }}</span> in use<template v-if="scheduler.paused"
            ><span aria-hidden="true"> · </span><span class="agents__paused">Paused</span></template
          >
        </p>
        <p class="agents__note">Agents waiting in <code>omo ai wait</code> hold no slot.</p>
        <p v-if="agents.error.value" class="agents__error" role="alert" data-agents-error>{{ agents.error.value }}</p>
      </div>
      <ul v-if="sessions.length > 0" ref="list" class="agents__list" aria-label="Running agents">
        <li v-for="s in sessions" :key="s.id"><AgentRow :session="s" @open="show" /></li>
      </ul>
      <p v-else-if="agents.loaded.value" class="agents__muted" data-agents-empty>No agent is running.</p>
    </template>
  </div>
</template>

<style scoped>
.agents {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
}
.agents__head {
  display: grid;
  gap: var(--omo-space);
}
.agents__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.agents__slots,
.agents__who {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 calc(var(--omo-space) * 1.5);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.agents__slots {
  display: block;
}
.agents__count {
  color: var(--omo-accent);
}
.agents__paused {
  color: var(--omo-warn);
}
.agents__note,
.agents__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.agents__note code {
  color: var(--omo-fg);
}
.agents__error,
.agents__ended {
  margin: 0;
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.agents__link {
  color: var(--omo-accent);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.agents__list {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
</style>

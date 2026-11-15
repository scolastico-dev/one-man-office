<script setup lang="ts">
// The CEO tab. Only the user's choice of the tab may start the CEO, and only
// once per choice: the tab takes the sidebar's one-shot choice, so a tab
// restored from storage or mounted again (collapse and expand, narrow and
// wide) never starts it and a reload does not spend tokens. A CEO that is
// already running is attached without a request. Whatever the server refuses
// is shown in its own words.
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { agentTerminalPath, newCEOSession, openCEO } from '../../api/endpoints'
import type { AgentData, AgentSession, ApiEvent } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { LazyTerminal } from '../terminal/LazyTerminal'
import { useAgents } from './useAgents'
import { useSidebar } from './useSidebar'

/** How long a start may take before the tab says so. */
const SLOW_MS = 400

const session = useSession()
const agents = useAgents()
const sidebar = useSidebar()
const announcer = useAnnouncer()

/** The CEO session shown: the live one from the list, or the one a request answered. */
const current = shallowRef<AgentSession | null>(null)
const ended = ref(false)
const busy = ref(false)
const slow = ref(false)
const failure = ref('')
const askNew = ref(false)
const newError = ref('')
let slowTimer: ReturnType<typeof setTimeout> | null = null

const live = computed(() => agents.sessions.value.find((s) => s.role === 'ceo') ?? null)
const terminalId = computed(() => current.value?.terminal_id ?? '')

function adopt(s: AgentSession) {
  const keep = current.value?.id === s.id ? current.value.terminal_id : undefined
  current.value = s.terminal_id === undefined && keep !== undefined ? { ...s, terminal_id: keep } : s
  ended.value = false
  failure.value = ''
}

watch(
  live,
  (s) => {
    if (s) adopt(s)
  },
  { immediate: true },
)

const off = session.store.onEvent('agent.ended', (ev: ApiEvent) => {
  const s = (ev.data as AgentData | undefined)?.agent
  if (!s || s.role !== 'ceo') return
  // The old session ends while a new one is being started: that is no ending.
  if (busy.value || current.value?.id !== s.id) return
  current.value = null
  ended.value = true
})
onBeforeUnmount(() => {
  off()
  clearSlow()
})

function clearSlow() {
  if (slowTimer !== null) clearTimeout(slowTimer)
  slowTimer = null
  slow.value = false
}

async function run(send: () => Promise<{ data: AgentSession }>): Promise<AgentSession | null> {
  if (busy.value) return null
  busy.value = true
  failure.value = ''
  slowTimer = setTimeout(() => (slow.value = true), SLOW_MS)
  try {
    const { data } = await send()
    adopt(data)
    return data
  } catch (e) {
    const text = e instanceof Error && e.message ? e.message : 'The CEO could not be started.'
    throw Object.assign(new Error(text), { cause: e })
  } finally {
    busy.value = false
    clearSlow()
  }
}

async function start() {
  try {
    await run(() => openCEO(session.client))
  } catch (e) {
    failure.value = (e as Error).message
    announcer.assertive(failure.value)
  }
}

async function confirmNew() {
  newError.value = ''
  try {
    await run(() => newCEOSession(session.client))
    askNew.value = false
  } catch (e) {
    newError.value = (e as Error).message
    announcer.assertive(newError.value)
  }
}

// The user chose the tab: the CEO starts once it is known that none is
// running. The choice is taken when it is made, also before the list is in.
const wanted = ref(false)
watch(
  () => sidebar.chosen.value,
  () => {
    if (sidebar.takeChoice('ceo')) wanted.value = true
  },
  { immediate: true },
)
watch(
  [wanted, () => agents.loaded.value, live],
  () => {
    if (!wanted.value || !agents.loaded.value) return
    wanted.value = false
    if (!live.value && current.value === null && !busy.value) void start()
  },
  { immediate: true },
)

const view = computed<'live' | 'starting' | 'failed' | 'ended' | 'idle' | 'wait'>(() => {
  if (busy.value) return slow.value ? 'starting' : current.value && terminalId.value ? 'live' : 'wait'
  if (failure.value) return 'failed'
  if (current.value) return terminalId.value ? 'live' : 'starting'
  return ended.value ? 'ended' : 'idle'
})
</script>

<template>
  <div class="ceo" data-ceo-tab>
    <template v-if="view === 'live'">
      <div class="ceo__bar">
        <p class="ceo__note">Typing here types into the CEO.</p>
        <UiButton size="sm" data-ceo-new @click="((askNew = true), (newError = ''))">New session…</UiButton>
      </div>
      <p v-if="sidebar.covered.value" class="ceo__note" data-terminal-covered>The terminal shows here again when the ticket's panel closes.</p>
      <div v-else data-ceo-live>
        <LazyTerminal :key="terminalId" :src="agentTerminalPath(terminalId)" label="Terminal of the CEO" ended-text="The CEO session ended." />
      </div>
    </template>
    <p v-else-if="view === 'starting'" class="ceo__note" role="status">Starting the CEO…</p>
    <template v-else-if="view === 'failed'">
      <p class="ceo__error" role="alert">{{ failure }}</p>
      <div><UiButton size="sm" @click="start">Try again</UiButton></div>
    </template>
    <template v-else-if="view === 'ended'">
      <p class="ceo__note" role="status">The CEO session ended.</p>
      <div>
        <UiButton size="sm" variant="primary" @click="start">Start the CEO</UiButton>
      </div>
    </template>
    <template v-else-if="view === 'idle'">
      <p class="ceo__note">The CEO is not running.</p>
      <div>
        <UiButton size="sm" variant="primary" @click="start">Start the CEO</UiButton>
      </div>
    </template>

    <UiDialog :open="askNew" title="Start a new CEO session?" eyebrow="CEO" @close="askNew = false">
      <p class="ceo__note">End the CEO's current session and start a fresh one? The new CEO reads the open tickets it supervises.</p>
      <p v-if="newError" class="ceo__error" role="alert">{{ newError }}</p>
      <template #actions>
        <UiButton @click="askNew = false">Keep the current session</UiButton>
        <UiButton variant="primary" :disabled="busy" data-primary @click="confirmNew">Start a new session</UiButton>
      </template>
    </UiDialog>
  </div>
</template>

<style scoped>
.ceo {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
}
.ceo__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.ceo__note,
.ceo__error {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.ceo__note {
  color: var(--omo-muted);
}
.ceo__error {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  color: var(--omo-fg);
}
</style>

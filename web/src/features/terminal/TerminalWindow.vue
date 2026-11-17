<script setup lang="ts">
// One terminal alone, filling its own browser window (`/terminal/<kind>/<id>`).
// A slim title line says what it is and how it is connected and offers the
// way back. The window tells the app's other windows it is open (they show a
// placeholder instead of a second view), remembers its size for the next
// window of its kind, and closes itself when another window brings the
// terminal back. Lock screen, refused token and a restarting omo work as in
// the main window: this page is kept under the app's lock screen like the
// board.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { listAgents } from '../../api/endpoints'
import type { AgentSession } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { boardLocation } from '../../locations'
import { useSession } from '../../session'
import { ROLE_LABELS, shortenMiddle } from '../board/model'
import { useAgents } from '../sidebar/useAgents'
import { useTerminals } from '../sidebar/useTerminals'
import { LazyTerminal } from './LazyTerminal'
import { announceTerminalWindow, browserTerminalWindowDeps, writeWindowSize, type TerminalWindowPresence } from './popout'
import { srcOf, type TerminalTarget, type WindowSizeKind } from './terminalTarget'

const RESIZE_SAVE_MS = 300
/** How long the window waits for the browser to close it before it says it could not. */
const CLOSE_CHECK_MS = 300

const route = useRoute()
const session = useSession()

const target = computed<TerminalTarget>(() => ({
  kind: route.params.kind === 'user' ? 'user' : 'agent',
  id: String(route.params.id ?? ''),
}))

// What the terminal is: an agent (live from the agent list, or found among
// the ended ones) or one of the user's shells.
const agents = target.value.kind === 'agent' ? useAgents() : null
const shells = target.value.kind === 'user' ? useTerminals() : null
const endedAgent = ref<AgentSession | null>(null)
const lookedUp = ref(false)
const lastAgent = ref<AgentSession | null>(null)

const liveAgent = computed(() => agents?.sessions.value.find((s) => s.terminal_id === target.value.id) ?? null)
watch(liveAgent, (s) => {
  if (s) lastAgent.value = s
})
const agent = computed(() => liveAgent.value ?? lastAgent.value ?? endedAgent.value)
const shell = computed(() => shells?.terminals.value.find((t) => t.id === target.value.id) ?? null)

// An agent that is not live (ended before this window opened, or a window
// opened by address later) is looked for once among all agents.
watch(
  () => agents?.loaded.value || agents?.error.value,
  (done) => {
    if (!done || !agents || lookedUp.value) return
    if (liveAgent.value) {
      lookedUp.value = true
      return
    }
    listAgents(session.client, false).then(
      ({ data }) => {
        endedAgent.value = data.find((s) => s.terminal_id === target.value.id) ?? null
        lookedUp.value = true
      },
      () => (lookedUp.value = true),
    )
  },
  { immediate: true },
)

/** The terminal is shown once it is known what it is, so its name is right from the start. */
const ready = computed(() => (shells ? shells.loaded.value || shells.error.value !== '' : lookedUp.value))

const title = computed(() => {
  if (shells) {
    const t = shell.value
    return t ? `${visibleText(t.title)} — ${visibleText(shortenMiddle(t.dir, 60))}` : 'Terminal'
  }
  const s = agent.value
  if (!s) return 'Agent terminal'
  if (s.role === 'ceo') return 'CEO'
  return `${ROLE_LABELS[s.role] ?? s.role} ${visibleText(s.name)}${s.ticket === null ? '' : ` on #${s.ticket}`}`
})

const label = computed(() => {
  if (shells) return shell.value ? `Terminal ${visibleText(shell.value.title)}` : 'Terminal'
  const s = agent.value
  if (!s) return 'Terminal of an agent'
  if (s.role === 'ceo') return 'Terminal of the CEO'
  return `Terminal of ${title.value}`
})

const endedText = computed(() => (shells ? 'The shell exited.' : agent.value?.role === 'ceo' ? 'The CEO session ended.' : 'This agent ended.'))
const sizeKind = computed<WindowSizeKind>(() => (shells ? 'user' : agent.value?.role === 'ceo' ? 'ceo' : 'agent'))

watch(
  title,
  (t) => {
    document.title = `${t} · omo`
  },
  { immediate: true },
)

const ended = ref(false)
/** The terminal was brought back to another window, but this one could not close itself. */
const returned = ref(false)
let presence: TerminalWindowPresence | null = null

function onState(state: string) {
  if (state === 'ended' && !ended.value) {
    ended.value = true
    presence?.ended()
  }
}

/** Closes this window; a window the browser does not let a page close goes to the board instead. */
function leave(after: () => void): void {
  window.close()
  setTimeout(() => {
    if (!window.closed) after()
  }, CLOSE_CHECK_MS)
}

function returnToOmo() {
  leave(() => {
    presence?.dispose()
    presence = null
    window.location.assign(boardHref.value)
  })
}

const boardHref = computed(() => boardLocation({ query: {} }).path)

let resizeTimer: ReturnType<typeof setTimeout> | undefined
function onResize() {
  clearTimeout(resizeTimer)
  resizeTimer = setTimeout(() => {
    writeWindowSize(() => window.localStorage, sizeKind.value, { width: window.innerWidth, height: window.innerHeight })
  }, RESIZE_SAVE_MS)
}

onMounted(() => {
  presence = announceTerminalWindow(target.value, browserTerminalWindowDeps())
  presence.onCloseAsked(() =>
    leave(() => {
      // Shown in the other window now: this one lets go of the terminal.
      returned.value = true
      presence?.dispose()
      presence = null
    }),
  )
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  clearTimeout(resizeTimer)
  window.removeEventListener('resize', onResize)
  presence?.dispose()
  presence = null
})
</script>

<template>
  <main class="tw" data-terminal-window :data-kind="target.kind">
    <template v-if="returned">
      <h1 class="tw__heading">{{ title }}</h1>
      <p class="tw__note" role="status" data-terminal-returned>This terminal shows in the main window again.</p>
      <p class="tw__note"><RouterLink :to="boardLocation({ query: {} })">Go to the board</RouterLink></p>
    </template>
    <template v-else-if="ready">
      <LazyTerminal :key="target.id" :src="srcOf(target)" :label="label" :ended-text="endedText" :popout="false" fill autofocus @state="onState">
        <template #lead>
          <h1 class="tw__title" data-terminal-title>{{ title }}</h1>
        </template>
        <template #trail>
          <UiButton size="sm" data-terminal-return @click="returnToOmo">Return to omo</UiButton>
        </template>
      </LazyTerminal>
      <p v-if="ended" class="tw__ended" data-terminal-window-ended>
        <UiButton size="sm" variant="primary" data-terminal-close-window @click="returnToOmo">Close this window</UiButton>
      </p>
    </template>
    <template v-else>
      <h1 class="sr-only">{{ title }}</h1>
      <p class="tw__note" role="status">Loading the terminal</p>
    </template>
  </main>
</template>

<style scoped>
/* The terminal fills the window; nothing else takes room but the title line. */
.tw {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 2);
  /* Exactly the window and clipped (see AppShell): the document never scrolls. */
  position: relative;
  height: 100dvh;
  min-height: 0;
  overflow: clip;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) calc(var(--omo-space) * 3);
  background: var(--omo-bg);
}
.tw__title {
  /* Shrinks with an ellipsis rather than pushing the line into two: the terminal keeps its rows. */
  flex: 1 1 0;
  min-width: 0;
  margin: 0 calc(var(--omo-space) * 2) 0 0;
  overflow: hidden;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  font-weight: 600;
  line-height: var(--omo-text-sm-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tw__heading {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.tw__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tw__ended {
  margin: 0;
}
</style>

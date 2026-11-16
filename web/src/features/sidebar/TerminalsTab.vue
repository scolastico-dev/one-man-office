<script setup lang="ts">
// The Terminals tab: the user's own shells. A shell opens in the home folder or
// in a project's folder (New terminal: a search over the enabled projects, with
// Home folder first) or in a ticket's worktree (the ticket's Open terminal action)
// and keeps running when the browser closes. A row shows a shell inside the tab
// with a way back; closing a running shell asks first. Everything the server
// refuses is shown here in its own words. Titles and folders are free text and
// shown as text.
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { userTerminalPath } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { OpenTerminalRequest, UserTerminal } from '../../api/types'
import RelativeTime from '../../components/ui/RelativeTime.vue'
import UiButton from '../../components/ui/UiButton.vue'
import SearchPicker, { type PickItem } from '../../components/ui/SearchPicker.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'
import { useBoard } from '../../store'
import { shortenMiddle } from '../board/model'
import { LazyTerminal } from '../terminal/LazyTerminal'
import { useSidebar } from './useSidebar'
import { useTerminals } from './useTerminals'
import TicketLink from '../ticket/TicketLink.vue'

/** The id of the shell to show, when another feature opened the tab on one. */
const props = defineProps<{ selected: string | null }>()

const store = useBoard()
const sidebar = useSidebar()
const say = useAnnouncer()
const shells = useTerminals()

const terminals = shells.terminals
const shown = computed(() => (props.selected === null ? null : (terminals.value.find((t) => t.id === props.selected) ?? null)))

/** The id of the Home folder entry; project ids are kept apart with a prefix. */
const HOME = 'home'
const projectPick = (id: string) => `project:${id}`

const places = computed<PickItem[]>(() => [
  { id: HOME, label: 'Home folder', detail: '~', always: true },
  ...[...store.state.projects.values()]
    .filter((p) => p.enabled && !p.missing)
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
    .map((p) => ({ id: projectPick(p.id), label: visibleText(p.name), detail: visibleText(p.path), detailShown: visibleText(shortenMiddle(p.path, 40)) })),
])

const projectCount = (n: number, all: number) => (n === all ? (all === 1 ? '1 project' : `${all} projects`) : `${n} of ${all} projects`)

function request(pick: string): OpenTerminalRequest {
  return pick === HOME ? { home: true } : { project: pick.slice(projectPick('').length) }
}

/** The refusal of the last request, in the server's words. */
const error = ref('')
const busy = ref(false)
const confirming = ref(false)

const list = ref<HTMLElement>()
const back = ref<InstanceType<typeof UiButton>>()
const confirmButton = ref<InstanceType<typeof UiButton>>()
const terminal = ref<{ focus?: () => void } | null>(null)
/** The terminal takes the keyboard once it is connected, after the user chose or opened a shell. */
let wantFocus = false

watch(
  () => props.selected,
  () => {
    confirming.value = false
    error.value = ''
  },
)

const folder = (t: UserTerminal) => visibleText(shortenMiddle(t.dir, 44))
const holdNote = (t: UserTerminal) => (t.ticket !== null && t.state === 'running' ? `Keeps the worktree of #${t.ticket} until it is closed.` : '')
const stateWord = (t: UserTerminal) => (t.state === 'running' ? 'running' : 'exited')

function fail(e: unknown, fallback: string) {
  error.value = failureText(e, { lost: `${fallback} omo did not answer; check the list before trying again.`, refused: fallback })
  say.assertive(error.value)
}

async function create(pick: string) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const t = await shells.open(request(pick))
    wantFocus = true
    sidebar.select(t.id)
    void nextTick(() => (back.value?.$el as HTMLElement | undefined)?.focus())
  } catch (e) {
    fail(e, 'The terminal could not be opened.')
  } finally {
    busy.value = false
  }
}

const comesFrom = ref<string | null>(null)

function show(t: UserTerminal) {
  comesFrom.value = t.id
  wantFocus = true
  sidebar.select(t.id)
  void nextTick(() => (back.value?.$el as HTMLElement | undefined)?.focus())
}

function leave() {
  sidebar.select(null)
  const id = comesFrom.value
  void nextTick(() => {
    const row = id === null ? null : list.value?.querySelector<HTMLElement>(`[data-terminal="${CSS.escape(id)}"]`)
    ;(row ?? list.value?.querySelector<HTMLElement>('button'))?.focus()
  })
}

async function end(t: UserTerminal) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await shells.close(t.id)
    confirming.value = false
    say.polite(t.state === 'running' ? 'Terminal closed.' : 'Terminal removed.')
    comesFrom.value = null
    leave()
  } catch (e) {
    fail(e, t.state === 'running' ? 'The terminal could not be closed.' : 'The terminal could not be removed.')
  } finally {
    busy.value = false
  }
}

function ask(t: UserTerminal) {
  if (t.state !== 'running') void end(t)
  else {
    confirming.value = true
    void nextTick(() => (confirmButton.value?.$el as HTMLElement | undefined)?.focus())
  }
}

function onState(state: string) {
  if (state === 'live' && wantFocus) {
    wantFocus = false
    terminal.value?.focus?.()
  }
}

const label = (t: UserTerminal) => `Terminal ${visibleText(t.title)}`
const Terminal: Component = LazyTerminal
</script>

<template>
  <div class="terminals">
    <template v-if="selected !== null">
      <div class="terminals__bar">
        <UiButton ref="back" size="sm" data-terminal-back @click="leave">Back</UiButton>
        <TicketLink v-if="shown && shown.ticket !== null" class="terminals__link" :number="shown.ticket" data-terminal-ticket>Open #{{ shown.ticket }}</TicketLink>
        <UiButton v-if="shown" size="sm" :variant="shown.state === 'running' ? 'danger' : 'default'" :disabled="busy || confirming" data-terminal-close @click="ask(shown)">{{
          shown.state === 'running' ? 'Close…' : 'Remove'
        }}</UiButton>
      </div>
      <template v-if="shown">
        <p class="terminals__who">
          <span class="terminals__title">{{ visibleText(shown.title) }}</span>
          <span class="terminals__muted">{{ stateWord(shown) }}</span>
        </p>
        <p class="terminals__muted terminals__dir" :title="visibleText(shown.dir)">{{ visibleText(shown.dir) }}</p>
        <p v-if="holdNote(shown)" class="terminals__muted" data-terminal-hold>{{ holdNote(shown) }}</p>
        <p v-if="shown.ticket !== null" class="terminals__muted" data-terminal-agent-files>The files in this folder are written by #{{ shown.ticket }}'s agents; check what a command runs before you run it here.</p>
        <div v-if="confirming" class="terminals__confirm" role="group" aria-label="Close this terminal" data-terminal-confirm>
          <p>Close this terminal? The shell and everything running in it stop.</p>
          <div class="terminals__actions">
            <UiButton ref="confirmButton" size="sm" variant="danger" :disabled="busy" data-terminal-confirm-yes @click="end(shown)">Close terminal</UiButton>
            <UiButton size="sm" :disabled="busy" @click="confirming = false">Keep it open</UiButton>
          </div>
        </div>
        <p v-if="error" class="terminals__error" role="alert" data-terminal-error>{{ error }}</p>
        <p v-if="shown.state === 'exited' && shown.exit_error" class="terminals__muted" data-terminal-exit>{{ visibleText(shown.exit_error) }}</p>
        <p v-if="sidebar.covered.value" class="terminals__muted" data-terminal-covered>The terminal shows here again when the ticket's panel closes.</p>
        <component :is="Terminal" v-else ref="terminal" :key="shown.id" :src="userTerminalPath(shown.id)" :label="label(shown)" ended-text="The shell exited." fill @state="onState" />
      </template>
      <p v-else-if="shells.loaded.value" class="terminals__ended" role="status" data-terminal-gone>This terminal is closed.</p>
      <p v-if="!shown && error" class="terminals__error" role="alert" data-terminal-error>{{ error }}</p>
    </template>

    <template v-else>
      <div class="terminals__head">
        <p class="terminals__note">Your terminals keep running when the browser closes.</p>
        <SearchPicker label="New terminal" search-label="Search projects" placeholder="Project name or folder" :items="places" empty="No project matches." :count-text="projectCount" size="sm" :disabled="busy" @select="create" />
        <p v-if="error" class="terminals__error" role="alert" data-terminal-error>{{ error }}</p>
        <p v-if="shells.error.value" class="terminals__error" role="alert" data-terminals-error>{{ shells.error.value }}</p>
      </div>
      <ul v-if="terminals.length > 0" ref="list" class="terminals__list" aria-label="Terminals">
        <li v-for="t in terminals" :key="t.id">
          <button type="button" class="trow" :title="`Show ${visibleText(t.title)}`" data-terminal-row :data-terminal="t.id" @click="show(t)">
            <span class="trow__top">
              <span class="trow__title">{{ visibleText(t.title) }}</span>
              <span class="trow__word">{{ stateWord(t) }}</span>
              <RelativeTime class="trow__since" :at="t.started_at" />
            </span>
            <span class="trow__dir">{{ folder(t) }}</span>
            <span v-if="holdNote(t)" class="trow__dir">{{ holdNote(t) }}</span>
          </button>
        </li>
      </ul>
      <p v-else-if="shells.loaded.value" class="terminals__muted" data-terminals-empty>No terminal is open.</p>
    </template>
  </div>
</template>

<style scoped>
/* A column as tall as the tab at least: an open terminal takes the room left. */
.terminals {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  min-height: 100%;
  padding: calc(var(--omo-space) * 3);
}
.terminals__head {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
}
.terminals__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.terminals__bar [data-terminal-close] {
  margin-left: auto;
}
.terminals__who {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 calc(var(--omo-space) * 1.5);
  min-width: 0;
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.terminals__title {
  min-width: 0;
  overflow-wrap: anywhere;
}
.terminals__dir {
  overflow-wrap: anywhere;
}
.terminals__note,
.terminals__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.terminals__error,
.terminals__ended,
.terminals__confirm {
  margin: 0;
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.terminals__confirm {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
.terminals__confirm p {
  margin: 0;
}
.terminals__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.terminals__link {
  color: var(--omo-accent);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.terminals__list {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.trow {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  width: 100%;
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
  background: transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  cursor: pointer;
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
.trow:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
@media (hover: hover) {
  .trow:hover {
    border-left-color: var(--omo-accent);
    background: var(--omo-accent-soft);
  }
}
.trow__top {
  display: flex;
  align-items: baseline;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.trow__title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.trow__word,
.trow__since,
.trow__dir {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.trow__word {
  flex-shrink: 0;
}
.trow__since {
  flex-shrink: 0;
  margin-left: auto;
}
.trow__dir {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

<script setup lang="ts">
// The ticket's place over the board, at `/<n>`: the head with the actions
// that apply now, the description (rendered, editable in place), the fields
// beside it (slot `fields`) and room for more below (slot `tabs`).
//
// The head shows at once from the board's summary, which the event stream
// keeps current; the description loads behind it and follows the stream.
// Nothing the user is typing is replaced by a change from elsewhere, and the
// panel does not close while there is unsaved text without asking.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '../../api/client'
import { getTicket } from '../../api/endpoints'
import type { DescriptionChangedData, DescriptionVersion, TicketSummary } from '../../api/types'
import IconButton from '../../components/ui/IconButton.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import DescriptionEditor from './DescriptionEditor.vue'
import DescriptionHistoryDialog from './DescriptionHistoryDialog.vue'
import DescriptionView from './DescriptionView.vue'
import { registerExportAction } from './exportAction'
import TicketActions from './TicketActions.vue'
import TicketFields from './TicketFields.vue'
import TicketHeader from './TicketHeader.vue'

registerExportAction()

const props = defineProps<{ number: number }>()
const emit = defineEmits<{ close: [] }>()

/** Loading text appears only when loading takes longer than this. */
const LOADING_DELAY_MS = 400

const store = useBoard()
const session = useSession()
const router = useRouter()
const titleId = useId()

type LoadState = 'loading' | 'ready' | 'missing' | 'forbidden' | 'failed'
const loadState = ref<LoadState>('loading')
const loadError = ref('')
const slow = ref(false)
/** The summary read with the description, for a ticket the board has not loaded. */
const fetched = shallowRef<TicketSummary | null>(null)
/** The newest description known: from the read, then from the stream. */
const desc = shallowRef<{ text: string; version: number } | null>(null)
/** A description saved here, shown until the stream has delivered it. */
const saved = shallowRef<{ text: string; eventId: number } | null>(null)

const editing = ref(false)
const historyOpen = ref(false)
const descDirty = ref(false)
const titleDirty = ref(false)
/** The user asked to close or leave with unsaved text; the panel asks first. */
const confirmLeave = shallowRef<null | { then: () => void }>(null)
let leaving = false

const dialog = ref<HTMLDialogElement>()
const header = ref<InstanceType<typeof TicketHeader>>()
const keepButton = ref<InstanceType<typeof UiButton>>()

const ticket = computed<TicketSummary | null>(() => store.view(props.number) ?? fetched.value)
const dirty = computed(() => (editing.value && descDirty.value) || titleDirty.value)
const shownText = computed(() => {
  const s = saved.value
  if (s !== null && store.state.lastEventId < s.eventId) return s.text
  return desc.value?.text ?? ''
})
const readonly = computed(() => ticket.value?.column === 'done')

// Loading

let loadToken = 0
async function load() {
  const my = ++loadToken
  const n = props.number
  try {
    const res = await getTicket(session.client, n)
    if (my !== loadToken) return
    const { description, ...summary } = res.data
    // A ticket the board lacks joins it so the stream keeps it current, unless it is an older Done one:
    // that would leave a gap in the Done pages. Its panel shows the ticket as read.
    if (store.view(n) === undefined && summary.column !== 'done') store.mergeTickets({ tickets: [summary], event_id: 0 })
    fetched.value = summary
    if (desc.value === null || summary.description_version >= desc.value.version) {
      desc.value = { text: description, version: summary.description_version }
    }
    loadState.value = 'ready'
  } catch (e) {
    if (my !== loadToken) return
    if (e instanceof ApiError && e.status === 404) loadState.value = 'missing'
    else if (e instanceof ApiError && e.status === 403) loadState.value = 'forbidden'
    else {
      loadState.value = 'failed'
      loadError.value = e instanceof Error && e.message ? e.message : 'The ticket could not be loaded.'
    }
  }
}

const offDescription = store.onEvent('description.changed', (ev) => {
  const d = ev.data as DescriptionChangedData | null
  if (d?.ticket !== props.number || typeof d.version?.text !== 'string') return
  if (desc.value === null || d.version.number > desc.value.version) desc.value = { text: d.version.text, version: d.version.number }
})
// After a resync events may have been skipped; after a reconnect the stream resumes, but a read costs little.
const offSnapshot = store.onSnapshot(() => void load())
watch(
  () => store.connection.value,
  (now, before) => {
    if (now === 'live' && before !== 'live') void load()
  },
)
// The summary names a newer version than the panel has: read it.
watch(
  () => ticket.value?.description_version,
  (v) => {
    if (v !== undefined && desc.value !== null && v > desc.value.version) void load()
  },
)
// A ticket that became Done is read-only; an edit without changes ends.
watch(readonly, (ro) => {
  if (ro && editing.value && !descDirty.value) editing.value = false
})

let slowTimer: ReturnType<typeof setTimeout> | undefined
let opener: HTMLElement | null = null

onMounted(() => {
  slowTimer = setTimeout(() => (slow.value = true), LOADING_DELAY_MS)
  void load()
  const el = dialog.value
  if (!el) return
  const active = document.activeElement
  opener = active instanceof HTMLElement && active !== document.body ? active : null
  if (!el.open) el.showModal()
  void nextTick(() => el.querySelector<HTMLElement>('[data-ticket-title], [data-panel-heading]')?.focus())
})

function onBeforeUnload(e: BeforeUnloadEvent) {
  if (!dirty.value) return
  e.preventDefault()
  e.returnValue = ''
}
window.addEventListener('beforeunload', onBeforeUnload)

function askFirst(then: () => void) {
  confirmLeave.value = { then }
  void nextTick(() => (keepButton.value?.$el as HTMLElement | undefined)?.focus())
}

// Leaving the ticket's address (back button, a link to another ticket) with unsaved text asks first.
const offGuard = router.beforeEach((to) => {
  if (leaving || !dirty.value || to.path === `/${props.number}`) return true
  askFirst(() => void router.push(to.fullPath))
  return false
})

onBeforeUnmount(() => {
  loadToken++
  clearTimeout(slowTimer)
  offDescription()
  offSnapshot()
  offGuard()
  window.removeEventListener('beforeunload', onBeforeUnload)
  const el = dialog.value
  if (el?.open) el.close()
  const back = opener
  const n = props.number
  // Focus goes back where the user came from, else to the ticket's card.
  queueMicrotask(() => {
    const active = document.activeElement
    if (active && active !== document.body && active.isConnected) return
    const target = back?.isConnected ? back : document.querySelector<HTMLElement>(`li[data-ticket="${n}"] [data-card-link]`)
    target?.focus()
  })
})

function requestClose() {
  if (dirty.value) askFirst(() => emit('close'))
  else emit('close')
}

function discardAndLeave() {
  const next = confirmLeave.value
  confirmLeave.value = null
  editing.value = false
  descDirty.value = false
  titleDirty.value = false
  header.value?.discard()
  leaving = true
  next?.then()
  void nextTick(() => (leaving = false))
}

function keepEditing() {
  confirmLeave.value = null
  void nextTick(() => dialog.value?.querySelector<HTMLElement>('[data-title-input], [data-description-editor] textarea')?.focus())
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape' || e.defaultPrevented) return
  e.preventDefault()
  if (confirmLeave.value) keepEditing()
  else requestClose()
}

// The browser's own close request: the panel decides.
function onCancel(e: Event) {
  e.preventDefault()
  requestClose()
}

// Closed by the browser regardless: stay open while there is unsaved text.
function onDialogClose() {
  const el = dialog.value
  if (!el || !el.isConnected || leaving) return
  if (dirty.value) {
    el.showModal()
    requestClose()
  } else emit('close')
}

// Description editing

function startEditing() {
  if (readonly.value || desc.value === null) return
  editing.value = true
}

function stopEditing() {
  editing.value = false
  descDirty.value = false
  void nextTick(() => dialog.value?.querySelector<HTMLElement>('[data-edit-description]')?.focus())
}

function onSaved(text: string, eventId: number | null, version: number) {
  // The next edit starts from what was just saved, whether or not the stream has said so yet.
  if (desc.value === null || version > desc.value.version) desc.value = { text, version }
  if (eventId !== null && store.state.lastEventId < eventId) saved.value = { text, eventId }
  stopEditing()
}

/** A version was restored from the history: the editor, if open, gives way to the restored text. */
function onRestored(v: DescriptionVersion, eventId: number | null) {
  onSaved(v.text, eventId, v.number)
  descDirty.value = false
}
</script>

<template>
  <dialog
    ref="dialog"
    class="tpanel"
    :aria-labelledby="titleId"
    aria-modal="true"
    data-ticket-panel
    @keydown="onKeydown"
    @cancel="onCancel"
    @close="onDialogClose"
  >
    <header class="tpanel__head">
      <div class="tpanel__head-top">
        <TicketHeader v-if="ticket" ref="header" :ticket="ticket" :title-id="titleId" @dirty="titleDirty = $event" />
        <div v-else class="min-w-0">
          <p class="tpanel__eyebrow">#{{ number }}</p>
          <h2 :id="titleId" class="tpanel__title" tabindex="-1" data-panel-heading>Ticket #{{ number }}</h2>
        </div>
        <IconButton label="Close" class="tpanel__close" data-close-panel @click="requestClose">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </IconButton>
      </div>
      <TicketActions v-if="ticket" :ticket="ticket" />
      <div v-if="confirmLeave" class="tpanel__leave" role="alertdialog" :aria-labelledby="`${titleId}-leave`" data-unsaved>
        <p :id="`${titleId}-leave`" class="tpanel__leave-text">You have unsaved changes. Leave without saving them?</p>
        <div class="tpanel__row">
          <UiButton ref="keepButton" size="sm" variant="primary" data-keep-editing @click="keepEditing">Keep editing</UiButton>
          <UiButton size="sm" variant="danger" data-discard @click="discardAndLeave">Discard changes</UiButton>
        </div>
      </div>
    </header>

    <div class="tpanel__scroll">
      <div v-if="!ticket && (loadState === 'missing' || loadState === 'forbidden')" class="tpanel__missing" data-ticket-missing>
        <p class="tpanel__text">
          {{ loadState === 'missing' ? `Ticket #${number} does not exist.` : `You may not see ticket #${number}.` }}
        </p>
        <UiButton variant="primary" @click="emit('close')">Back to the board</UiButton>
      </div>
      <div v-else-if="!ticket && loadState === 'failed'" class="tpanel__missing" role="alert">
        <p class="tpanel__text">{{ loadError }}</p>
        <div class="tpanel__row">
          <UiButton variant="primary" @click="load">Try again</UiButton>
          <UiButton @click="emit('close')">Back to the board</UiButton>
        </div>
      </div>
      <p v-else-if="!ticket" class="tpanel__text tpanel__muted" aria-live="polite">{{ slow ? `Loading #${number}…` : '' }}</p>

      <div v-else class="tpanel__grid">
        <section class="tpanel__main" :aria-labelledby="`${titleId}-desc`">
          <div class="tpanel__section-head">
            <h3 :id="`${titleId}-desc`" class="tpanel__h3">Description</h3>
            <div class="tpanel__row">
              <UiButton v-if="desc !== null" size="sm" data-open-history @click="historyOpen = true">
                History ({{ ticket.description_version }})
              </UiButton>
              <UiButton v-if="!editing && !readonly && desc !== null" size="sm" data-edit-description @click="startEditing">Edit</UiButton>
            </div>
          </div>
          <DescriptionEditor
            v-if="editing && desc !== null"
            :ticket="ticket"
            :current="desc"
            @close="stopEditing"
            @saved="onSaved"
            @dirty="descDirty = $event"
            @refresh="load"
          />
          <template v-else>
            <div v-if="desc === null && loadState === 'failed'" class="tpanel__row" role="alert">
              <p class="tpanel__text">{{ loadError }}</p>
              <UiButton size="sm" @click="load">Try again</UiButton>
            </div>
            <p v-else-if="desc === null" class="tpanel__text tpanel__muted">{{ slow ? 'Loading the description…' : '' }}</p>
            <DescriptionView v-else-if="shownText.trim() !== ''" :text="shownText" />
            <p v-else class="tpanel__text tpanel__muted" data-description-empty>No description yet.</p>
          </template>
        </section>
        <aside class="tpanel__side" :aria-labelledby="`${titleId}-fields`">
          <h3 :id="`${titleId}-fields`" class="tpanel__h3">Details</h3>
          <slot name="fields" :ticket="ticket">
            <TicketFields :ticket="ticket" />
          </slot>
        </aside>
      </div>
      <slot v-if="ticket" name="tabs" :ticket="ticket" />
      <DescriptionHistoryDialog v-if="ticket && historyOpen" open :ticket="ticket" :editing="editing" @close="historyOpen = false" @restored="onRestored" />
    </div>
  </dialog>
</template>

<style scoped>
.tpanel {
  width: calc(100% - var(--omo-space) * 8);
  max-width: 1120px;
  height: calc(100dvh - var(--omo-space) * 8);
  max-height: none;
  margin: auto;
  padding: 0;
  overflow: hidden;
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  color: var(--omo-fg);
}
.tpanel[open] {
  display: flex;
  flex-direction: column;
}
.tpanel::backdrop {
  background: var(--omo-backdrop);
}
.tpanel__head {
  display: grid;
  flex: none;
  gap: calc(var(--omo-space) * 3);
  max-height: 50dvh;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 6) calc(var(--omo-space) * 4);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.tpanel__head-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 4);
}
.tpanel__head-top > :first-child {
  flex: 1 1 auto;
  min-width: 0;
}
.tpanel__close {
  flex-shrink: 0;
  margin: calc(var(--omo-space) * -1.5) calc(var(--omo-space) * -2) 0 0;
}
.tpanel__eyebrow {
  margin: 0 0 var(--omo-space);
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tpanel__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
}
.tpanel__title:focus {
  outline: none;
}
.tpanel__leave {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.tpanel__leave-text {
  margin: 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.tpanel__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.tpanel__scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 6) calc(var(--omo-space) * 6);
}
.tpanel__grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(14rem, 18rem);
  gap: calc(var(--omo-space) * 8);
  align-items: start;
}
.tpanel__main,
.tpanel__side {
  min-width: 0;
}
.tpanel__side {
  padding-left: calc(var(--omo-space) * 6);
  border-left: var(--omo-border) solid var(--omo-line);
}
.tpanel__section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  min-height: calc(var(--omo-space) * 7);
  margin-bottom: calc(var(--omo-space) * 3);
}
.tpanel__h3 {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.tpanel__side .tpanel__h3 {
  display: flex;
  align-items: center;
  min-height: calc(var(--omo-space) * 7);
  margin-bottom: calc(var(--omo-space) * 3);
}
.tpanel__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tpanel__muted {
  color: var(--omo-muted);
}
.tpanel__missing {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 4);
}
@media (max-width: 767.98px) {
  .tpanel {
    width: 100%;
    max-width: none;
    height: 100dvh;
    border: 0;
  }
  .tpanel__head {
    max-height: 45dvh;
    padding: calc(var(--omo-space) * 4) calc(var(--omo-space) * 4) calc(var(--omo-space) * 3);
  }
  .tpanel__scroll {
    padding: calc(var(--omo-space) * 4);
  }
  .tpanel__grid {
    grid-template-columns: minmax(0, 1fr);
    gap: calc(var(--omo-space) * 6);
  }
  .tpanel__side {
    padding: calc(var(--omo-space) * 5) 0 0;
    border-top: var(--omo-border) solid var(--omo-line);
    border-left: 0;
  }
}
</style>

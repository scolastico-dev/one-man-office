// What the ticket's two views share, the panel over the board (`/<n>`) and
// the ticket's own page (/ticket/<n>): reading the ticket and its
// description and following the stream, editing the description, and asking
// before unsaved text is left behind.
//
// The head shows at once from the board's summary, which the event stream
// keeps current; the description loads behind it and follows the stream.
// Nothing the user is typing is replaced by a change from elsewhere. A
// description or comment being written is kept for the ticket (drafts.ts), so
// switching between the panel and the page of the same ticket goes ahead; a
// title being edited is not kept, and that switch asks first. Leaving the
// ticket with unsaved text asks first.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, toValue, watch, type ComputedRef, type MaybeRefOrGetter, type Ref, type ShallowRef } from 'vue'
import { useRouter, type RouteLocationNormalized } from 'vue-router'
import { ApiError } from '../../api/client'
import { getTicket } from '../../api/endpoints'
import type { DescriptionChangedData, DescriptionVersion, TicketSummary } from '../../api/types'
import { ticketLocation, ticketPageLocation } from '../../locations'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { ticketDrafts } from './drafts'
import { registerExportAction } from './exportAction'
import { usePluginActions } from './pluginActions'
import { registerStartAnywayAction, useStartAnywayUsage } from './startAnywayAction'

registerExportAction()
registerStartAnywayAction()

/** Loading text appears only when loading takes longer than this. */
export const LOADING_DELAY_MS = 400

export type TicketLoadState = 'loading' | 'ready' | 'missing' | 'forbidden' | 'failed'

export interface TicketView {
  readonly number: ComputedRef<number>
  loadState: Readonly<Ref<TicketLoadState>>
  loadError: Readonly<Ref<string>>
  /** Loading took long enough to say so. */
  slow: Readonly<Ref<boolean>>
  /** The ticket as the board knows it, else as read; null until read. */
  ticket: ComputedRef<TicketSummary | null>
  /** The newest description known: from the read, then from the stream. */
  desc: Readonly<ShallowRef<{ text: string; version: number } | null>>
  /** The description to show (one saved here until the stream has it). */
  shownText: ComputedRef<string>
  /** A Done ticket cannot be changed. */
  readonly: ComputedRef<boolean>
  editing: Ref<boolean>
  historyOpen: Ref<boolean>
  descDirty: Ref<boolean>
  titleDirty: Ref<boolean>
  /** Unsaved text would be lost by leaving. */
  dirty: ComputedRef<boolean>
  /** The user asked to leave with unsaved text; the view asks first. */
  confirmLeave: Readonly<ShallowRef<null | { then: () => void }>>
  load(): Promise<void>
  /** Leaves through `then`, after asking when there is unsaved text. */
  leave(then: () => void): void
  /** Drops the unsaved text and leaves as asked. */
  discardAndLeave(): void
  keepEditing(): void
  startEditing(): void
  stopEditing(): void
  onSaved(text: string, eventId: number | null, version: number): void
  onRestored(v: DescriptionVersion, eventId: number | null): void
  /** Set while leaving after the user agreed, so the guard lets it through. */
  readonly leaving: () => boolean
}

export interface TicketViewOptions {
  /** The view's root element: where the focus goes back to after an edit. */
  root: Readonly<Ref<HTMLElement | undefined>>
  /** Drops an edit of the title (the header's own draft). */
  discardTitle(): void
  /** Focuses the "Keep editing" button of the question. */
  focusQuestion(): void
}

/** Whether `to` shows ticket #n, in the panel or on its page. */
export function showsTicket(n: number, to: RouteLocationNormalized): boolean {
  return to.path === ticketLocation(n, to).path || to.path === ticketPageLocation(n, to).path
}

export function useTicketView(numberOf: MaybeRefOrGetter<number>, o: TicketViewOptions): TicketView {
  const number = computed(() => toValue(numberOf))
  const store = useBoard()
  const session = useSession()
  const router = useRouter()
  usePluginActions()
  useStartAnywayUsage()

  const loadState = ref<TicketLoadState>('loading')
  const loadError = ref('')
  const slow = ref(false)
  const fetched = shallowRef<TicketSummary | null>(null)
  const desc = shallowRef<{ text: string; version: number } | null>(null)
  /** A description saved here, shown until the stream has delivered it. */
  const saved = shallowRef<{ text: string; eventId: number } | null>(null)

  // A description edit begun in the ticket's other view goes on here.
  const editing = ref(ticketDrafts.description(number.value) !== undefined)
  const historyOpen = ref(false)
  const descDirty = ref(false)
  const titleDirty = ref(false)
  const confirmLeave = shallowRef<null | { then: () => void }>(null)
  let leaving = false

  const ticket = computed<TicketSummary | null>(() => store.view(number.value) ?? fetched.value)
  const dirty = computed(() => (editing.value && descDirty.value) || titleDirty.value)
  const shownText = computed(() => {
    const s = saved.value
    if (s !== null && store.state.lastEventId < s.eventId) return s.text
    return desc.value?.text ?? ''
  })
  const readonly = computed(() => ticket.value?.column === 'done')

  let loadToken = 0
  async function load() {
    const my = ++loadToken
    const n = number.value
    try {
      const res = await getTicket(session.client, n)
      if (my !== loadToken) return
      const { description, ...summary } = res.data
      // A ticket the board lacks joins it so the stream keeps it current, unless it is an older Done one:
      // that would leave a gap in the Done pages. Its view shows the ticket as read.
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
    if (d?.ticket !== number.value || typeof d.version?.text !== 'string') return
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
  // The summary names a newer version than the view has: read it.
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

  // Another ticket in the same view (the app keys its views by ticket, so only in tests): read it afresh.
  watch(number, () => {
    fetched.value = null
    desc.value = null
    saved.value = null
    loadState.value = 'loading'
    void load()
  })

  let slowTimer: ReturnType<typeof setTimeout> | undefined
  onMounted(() => {
    slowTimer = setTimeout(() => (slow.value = true), LOADING_DELAY_MS)
    void load()
  })

  function onBeforeUnload(e: BeforeUnloadEvent) {
    if (!dirty.value) return
    e.preventDefault()
    e.returnValue = ''
  }
  window.addEventListener('beforeunload', onBeforeUnload)

  function askFirst(then: () => void) {
    confirmLeave.value = { then }
    void nextTick(o.focusQuestion)
  }

  // Leaving the ticket (back button, a link to another ticket) with unsaved text asks first. Its other view
  // takes the description and comment drafts along; only a title being edited would be lost there.
  const offGuard = router.beforeEach((to, from) => {
    if (leaving || !dirty.value || to.path === from.path) return true
    if (showsTicket(number.value, to) && !titleDirty.value) return true
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
  })

  function leave(then: () => void) {
    if (dirty.value) askFirst(then)
    else then()
  }

  function discardAndLeave() {
    const next = confirmLeave.value
    confirmLeave.value = null
    editing.value = false
    descDirty.value = false
    titleDirty.value = false
    ticketDrafts.setDescription(number.value, null)
    o.discardTitle()
    leaving = true
    next?.then()
    void nextTick(() => (leaving = false))
  }

  function keepEditing() {
    confirmLeave.value = null
    void nextTick(() => o.root.value?.querySelector<HTMLElement>('[data-title-input], [data-description-editor] textarea')?.focus())
  }

  function startEditing() {
    if (readonly.value || desc.value === null) return
    editing.value = true
  }

  function stopEditing() {
    editing.value = false
    descDirty.value = false
    ticketDrafts.setDescription(number.value, null)
    void nextTick(() => o.root.value?.querySelector<HTMLElement>('[data-edit-description]')?.focus())
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

  return {
    number,
    loadState,
    loadError,
    slow,
    ticket,
    desc,
    shownText,
    readonly,
    editing,
    historyOpen,
    descDirty,
    titleDirty,
    dirty,
    confirmLeave,
    load,
    leave,
    discardAndLeave,
    keepEditing,
    startEditing,
    stopEditing,
    onSaved,
    onRestored,
    leaving: () => leaving,
  }
}

// One settings item on a page: loaded with its revision, edited as a draft,
// saved with If-Match, and kept up to date while the page is open.
//
// Settings are files, and they change under the page: a hand edit, another
// tab, an agent. The item is read again whenever an event says it may have
// changed (`watches`), after every board snapshot and when the event stream is
// live again after a break. Without unsaved edits the page simply shows the
// new value. With unsaved edits the draft stays and `conflict` holds what is
// stored now, until the user chooses `useTheirs()` or `keepMine()`; only then
// does a save carry the new revision, so nothing is saved over a change the
// page has not shown.
//
// Reads and saves can answer in any order. Each takes a number when it
// starts and an answer is applied only when nothing newer was applied
// before it; a save makes every read under way old, and reads asked for
// while a save runs wait for its end.
import { computed, getCurrentScope, nextTick, onScopeDispose, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { ApiError, fieldOf } from '../../api/client'
import { isLostAnswer } from '../../api/lostAnswer'
import { timeKey } from '../../api/time'
import type { ApiEvent, FileError, SettingsChangedData, SettingsInvalidData } from '../../api/types'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useBoard, type BoardStore } from '../../store'
import { askFirst } from './useLeaveGuard'

/** What `load` answers: the item, its revision, and the files of it that cannot be read. */
export interface SettingsLoaded<T> {
  value: T
  revision: string
  /** Broken files of this item; omo uses the last version it could read. */
  errors?: FileError[]
}

/** What `save` answers: the item as stored, its new revision, and the `Omo-Event-Id` of the write. */
export interface SettingsSaved<T> {
  value: T
  revision: string
  eventId: number | null
}

export interface SettingsResourceOptions<T> {
  /** Reads the item. */
  load(): Promise<SettingsLoaded<T>>
  /** Writes `draft`, sending `revision` as If-Match. A refusal is thrown as the client's `ApiError`. */
  save(draft: T, revision: string): Promise<SettingsSaved<T>>
  /** Whether an event may have changed the item: it is then read again. Also decides which `settings.invalid` and disk changes concern it. */
  watches(ev: ApiEvent): boolean
  /** Whether two values are the same; a deep comparison of the JSON values by default (key order does not matter). */
  equal?(a: T, b: T): boolean
  /**
   * The keys (dotted paths into T) a save writes, for a partial write such as
   * `PATCH /api/settings`. When a save is refused as stale and the stored
   * values of these keys still equal what the draft was built on, the save is
   * sent once more with the new revision instead of showing a conflict.
   * Without keys a stale save always shows the conflict.
   */
  keys?: readonly string[] | ((draft: T, base: T) => readonly string[])
  /** Asked before saving over a broken file; by default the question `BROKEN_QUESTION` in a dialog. */
  confirmBroken?(errors: readonly FileError[]): Promise<boolean>
}

/**
 * How a save ended. `saved`: stored. `unchanged`: nothing to save.
 * `refused`: the server said no; see `fieldErrors` and `error`. `conflict`:
 * the stored value changed; see `conflict`. `lost`: the answer was lost, the
 * item is read again. `cancelled`: the user declined to save over a broken
 * file. `busy`: a save is already running.
 */
export type SaveOutcome = 'saved' | 'unchanged' | 'refused' | 'conflict' | 'lost' | 'cancelled' | 'busy'

export interface SettingsResource<T> {
  /** The item as omo stored it, as far as the page knows; null until the first read. */
  value: Readonly<Ref<T | null>>
  /** The revision `value` was read with, sent as If-Match. */
  revision: Readonly<Ref<string | null>>
  /** The form's copy; edit it in place. Null until the first read. */
  draft: Ref<T | null>
  /** The draft differs from `value`. */
  dirty: ComputedRef<boolean>
  /** True until the first read answered (or failed). */
  loading: Readonly<Ref<boolean>>
  /** A save is running. */
  saving: Readonly<Ref<boolean>>
  /** The message for the top of the form: a failed read, a refusal naming no field, a lost answer. Empty when there is none. */
  error: Readonly<Ref<string>>
  /** The server's refusal by the field it names (`details.field`, a dotted key). */
  fieldErrors: Readonly<Ref<Readonly<Record<string, string>>>>
  /** What is stored now while it differs from what the draft was built on; null without a conflict. */
  conflict: Readonly<Ref<SettingsLoaded<T> | null>>
  /** Files of this item that cannot be read: from the read and from `settings.invalid`. */
  broken: ComputedRef<readonly FileError[]>
  /** When the item was last changed on disk rather than through omo (the event's time); null if not since the page opened. */
  changedOnDisk: Readonly<Ref<string | null>>
  /** For the Save bar: `conflict` while there is one, `saved` after a save until the next edit. */
  status: ComputedRef<'saved' | 'conflict' | null>
  /** Reads the item again. */
  reload(): Promise<void>
  /** Saves the draft; never throws. */
  save(): Promise<SaveOutcome>
  /** Drops the edits; with a conflict, takes the stored value. */
  discard(): void
  /** In a conflict: drops the draft and shows what is stored now. */
  useTheirs(): void
  /** In a conflict: keeps the draft, now based on what is stored (the next save overwrites it). */
  keepMine(): void
}

export interface SettingsResourceDeps {
  store: Pick<BoardStore, 'onEvent' | 'onSnapshot' | 'connection'>
  announce?: { polite(text: string): void; assertive(text: string): void }
}

/** Shown, and announced, after a save whose answer was lost. */
export const LOST_TEXT = 'The answer was lost; the page shows what omo stored.'
/** Shown when Save is pressed while a conflict waits for a choice. */
export const CHOOSE_FIRST_TEXT = 'This changed outside this page. Choose “Use the new value” or “Keep mine” first.'
/** Announced when the stored value changes under a draft. */
export const CONFLICT_TEXT = 'This changed outside this page (on disk or in another tab).'
/** Asked before saving over a file that cannot be read. */
export const BROKEN_QUESTION = {
  title: 'Save anyway?',
  text: 'Saving replaces the file on disk, including the edit that cannot be read.',
  confirm: 'Save anyway',
  cancel: 'Cancel',
}

/** "Changed on disk at 14:02, not through omo." for an event time. */
export function diskChangeText(at: string): string {
  const d = new Date(timeKey(at).slice(0, 23) + 'Z')
  const time = Number.isNaN(d.getTime()) ? at : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return `Changed on disk at ${time}, not through omo.`
}

/** "<file> cannot be read: <message>. omo uses the last version it could read." */
export function brokenText(e: FileError): string {
  return `${e.file} cannot be read: ${e.message.replace(/[.\s]+$/, '')}. omo uses the last version it could read.`
}

/** Deep equality of JSON values; key order does not matter, a missing key equals `undefined`. */
export function sameJSON(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  if (Array.isArray(a)) {
    const bb = b as unknown[]
    return a.length === bb.length && a.every((x, i) => sameJSON(x, bb[i]))
  }
  const ao = a as Record<string, unknown>
  const bo = b as Record<string, unknown>
  const keys = new Set([...Object.keys(ao), ...Object.keys(bo)])
  for (const k of keys) if (!sameJSON(ao[k], bo[k])) return false
  return true
}

/** The value at a dotted path (`health.silent_after`, `workspace_roots.1.ignore`). */
export function valueAt(v: unknown, path: string): unknown {
  let at = v
  for (const part of path.split('.')) {
    if (typeof at !== 'object' || at === null) return undefined
    at = (at as Record<string, unknown>)[part]
  }
  return at
}

const clone = <T,>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T))

const isStale = (e: unknown) => e instanceof ApiError && e.code === 'stale_settings'

const messageOf = (e: unknown, fallback: string) => (e instanceof Error && e.message !== '' ? e.message : fallback)

/** Moves the focus to the first field marked invalid inside `root`, after the page has drawn the errors. */
export async function focusFirstInvalid(root: HTMLElement | null | undefined): Promise<void> {
  await nextTick()
  root?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
}

/** The resource with explicit dependencies, for tests and for code outside a component. Call `dispose` when done. */
export function createSettingsResource<T>(o: SettingsResourceOptions<T>, deps: SettingsResourceDeps): SettingsResource<T> & { dispose(): void } {
  const eq = o.equal ?? ((a: T, b: T) => sameJSON(a, b))
  const announce = deps.announce ?? { polite: () => {}, assertive: () => {} }
  const confirmBroken = o.confirmBroken ?? (() => askFirst(BROKEN_QUESTION))

  const value = shallowRef<T | null>(null)
  const revision = ref<string | null>(null)
  const draft = ref<T | null>(null) as Ref<T | null>
  const loading = ref(true)
  const saving = ref(false)
  const error = ref('')
  let errorFromLoad = false
  const fieldErrors = shallowRef<Readonly<Record<string, string>>>({})
  const conflict = shallowRef<SettingsLoaded<T> | null>(null)
  const loadErrors = shallowRef<readonly FileError[]>([])
  const invalidSeen = shallowRef<ReadonlyMap<string, FileError>>(new Map())
  const changedOnDisk = ref<string | null>(null)
  const justSaved = ref(false)

  const dirty = computed(() => value.value !== null && draft.value !== null && !eq(draft.value, value.value))
  const broken = computed<readonly FileError[]>(() => {
    const out = [...loadErrors.value]
    for (const e of invalidSeen.value.values()) if (!out.some((x) => x.file === e.file)) out.push(e)
    return out
  })
  const status = computed(() => (conflict.value !== null ? 'conflict' : justSaved.value && !dirty.value ? 'saved' : null))
  const stopSavedWatch = watch(dirty, (d) => {
    if (d) justSaved.value = false
  })

  /** Numbers of reads and saves: an answer is applied only when it is newer than the last one applied. */
  let seq = 0
  let applied = 0
  let disposed = false
  /** The `Omo-Event-Id` of the last save: events up to it are already in the saved value. */
  let savedThrough = 0
  let rereadAfterSave = false

  function setBase(got: { value: T; revision: string }) {
    value.value = got.value
    revision.value = got.revision
  }

  function clearSaveError() {
    if (!errorFromLoad) error.value = ''
    fieldErrors.value = {}
  }

  function showConflict(got: SettingsLoaded<T>) {
    const first = conflict.value === null
    conflict.value = got
    if (first) announce.polite(CONFLICT_TEXT)
  }

  /** A read answered: show it, or hold it as the conflict while the draft differs. */
  function take(got: SettingsLoaded<T>) {
    loadErrors.value = got.errors ?? []
    if (errorFromLoad) {
      error.value = ''
      errorFromLoad = false
    }
    if (value.value === null || draft.value === null || !dirty.value) {
      setBase(got)
      draft.value = clone(got.value)
      conflict.value = null
      return
    }
    // The draft says what is stored now (a save whose answer was lost went through): nothing to choose.
    if (eq(got.value, draft.value)) {
      setBase(got)
      conflict.value = null
      return
    }
    // What the draft was built on is still stored (the file changed elsewhere, or back again).
    if (eq(got.value, value.value)) {
      revision.value = got.revision
      conflict.value = null
      return
    }
    showConflict(got)
  }

  async function readOnce() {
    const mine = ++seq
    try {
      const got = await o.load()
      if (disposed || mine <= applied) return
      applied = mine
      take(got)
    } catch (e) {
      if (disposed || mine <= applied) return
      if (error.value === '' || errorFromLoad) {
        error.value = messageOf(e, 'The settings could not be read.')
        errorFromLoad = true
      }
    } finally {
      if (!disposed) loading.value = false
    }
  }

  let running: Promise<void> | null = null
  let again = false

  /** One read at a time; asks while one runs are served by one more read after it. */
  function reload(): Promise<void> {
    if (disposed) return Promise.resolve()
    if (saving.value) {
      rereadAfterSave = true
      return Promise.resolve()
    }
    if (running !== null) {
      again = true
      return running
    }
    running = (async () => {
      do {
        again = false
        await readOnce()
      } while (again && !disposed && !saving.value)
      if (again && saving.value) rereadAfterSave = true
    })().finally(() => {
      running = null
    })
    return running
  }

  function resendable(fresh: SettingsLoaded<T>, base: T, sent: T): boolean {
    if (o.keys === undefined) return false
    const keys = typeof o.keys === 'function' ? o.keys(sent, base) : o.keys
    return keys.every((k) => sameJSON(valueAt(fresh.value, k), valueAt(base, k)))
  }

  async function save(): Promise<SaveOutcome> {
    if (disposed || value.value === null || draft.value === null || revision.value === null) return 'unchanged'
    if (saving.value) return 'busy'
    if (conflict.value !== null) {
      error.value = CHOOSE_FIRST_TEXT
      errorFromLoad = false
      announce.assertive(CHOOSE_FIRST_TEXT)
      return 'conflict'
    }
    if (!dirty.value) return 'unchanged'
    if (broken.value.length > 0 && !(await confirmBroken(broken.value))) return 'cancelled'
    if (disposed || saving.value) return 'busy'

    saving.value = true
    // Every read under way started before this save: its answer is old.
    applied = ++seq
    clearSaveError()
    justSaved.value = false
    const sent = clone(draft.value)
    const base = value.value
    try {
      let answer: SettingsSaved<T>
      try {
        answer = await o.save(sent, revision.value)
      } catch (e) {
        if (!isStale(e)) throw e
        const fresh = await o.load()
        if (disposed) return 'conflict'
        if (!resendable(fresh, base, sent)) {
          applied = ++seq
          loadErrors.value = fresh.errors ?? []
          showConflict(fresh)
          return 'conflict'
        }
        answer = await o.save(sent, fresh.revision)
      }
      if (disposed) return 'saved'
      applied = ++seq
      setBase(answer)
      conflict.value = null
      invalidSeen.value = new Map()
      // Edits made while the save ran stay; otherwise the form shows what was stored.
      if (draft.value !== null && eq(draft.value, sent)) draft.value = clone(answer.value)
      if (answer.eventId !== null) savedThrough = Math.max(savedThrough, answer.eventId)
      justSaved.value = true
      announce.polite('Saved.')
      return 'saved'
    } catch (e) {
      if (disposed) return 'refused'
      if (isLostAnswer(e)) {
        error.value = LOST_TEXT
        errorFromLoad = false
        announce.polite(LOST_TEXT)
        rereadAfterSave = true
        return 'lost'
      }
      if (isStale(e)) {
        // Refused as stale twice in a row: something keeps changing it.
        try {
          const fresh = await o.load()
          if (!disposed) {
            applied = ++seq
            loadErrors.value = fresh.errors ?? []
            showConflict(fresh)
          }
        } catch (le) {
          error.value = messageOf(le, 'The settings could not be read.')
          errorFromLoad = false
        }
        return 'conflict'
      }
      const message = messageOf(e, 'The change was refused.')
      const field = fieldOf(e)
      if (field !== null) fieldErrors.value = { [field]: message }
      else {
        error.value = message
        errorFromLoad = false
      }
      announce.assertive(message)
      return 'refused'
    } finally {
      saving.value = false
      if (rereadAfterSave && !disposed) {
        rereadAfterSave = false
        void reload()
      }
    }
  }

  function discard() {
    if (conflict.value !== null) {
      useTheirs()
      return
    }
    if (value.value !== null) draft.value = clone(value.value)
    clearSaveError()
  }

  function useTheirs() {
    const c = conflict.value
    if (c === null) return
    setBase(c)
    draft.value = clone(c.value)
    conflict.value = null
    clearSaveError()
  }

  function keepMine() {
    const c = conflict.value
    if (c === null) return
    setBase(c)
    conflict.value = null
    if (error.value === CHOOSE_FIRST_TEXT) error.value = ''
  }

  function onEvent(ev: ApiEvent) {
    if (!o.watches(ev)) return
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      if (d?.source === 'disk') changedOnDisk.value = ev.at
      if (invalidSeen.value.size > 0) invalidSeen.value = new Map()
    } else if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      if (d && typeof d.file === 'string') {
        const next = new Map(invalidSeen.value)
        next.set(d.file, { file: d.file, message: typeof d.message === 'string' ? d.message : '' })
        invalidSeen.value = next
      }
    }
    // Already in the value the last save answered with.
    if (ev.id <= savedThrough) return
    void reload()
  }

  const offEvent = deps.store.onEvent('*', onEvent)
  const offSnapshot = deps.store.onSnapshot(() => void reload())
  const stopLive = watch(
    () => deps.store.connection.value,
    (now, before) => {
      if (now === 'live' && before !== 'live') void reload()
    },
  )
  void reload()

  return {
    value,
    revision,
    draft,
    dirty,
    loading,
    saving,
    error,
    fieldErrors,
    conflict,
    broken,
    changedOnDisk,
    status,
    reload,
    save,
    discard,
    useTheirs,
    keepMine,
    dispose() {
      disposed = true
      offEvent()
      offSnapshot()
      stopLive()
      stopSavedWatch()
    },
  }
}

/**
 * The resource for the calling component: events come from the app's board
 * store, refusals and conflicts are announced, and everything stops when the
 * component unmounts. Pass `keys` for a partial write (see the options).
 *
 * ```ts
 * const limits = useSettingsResource({
 *   load: async () => { const { data } = await getSettings(client); return { value: { max_running_agents: data.settings.max_running_agents }, revision: data.revision, errors: data.errors } },
 *   save: async (d, rev) => { const { data, eventId } = await patchSettings(client, d, rev); return { value: { max_running_agents: data.settings.max_running_agents }, revision: data.revision, eventId } },
 *   watches: (ev) => ev.kind === 'settings.invalid' || (ev.kind === 'settings.changed' && (ev.data as SettingsChangedData).section === 'general'),
 *   keys: ['max_running_agents'],
 * })
 * ```
 */
export function useSettingsResource<T>(o: SettingsResourceOptions<T>): SettingsResource<T> {
  const r = createSettingsResource(o, { store: useBoard(), announce: useAnnouncer() })
  if (getCurrentScope()) onScopeDispose(r.dispose)
  return r
}

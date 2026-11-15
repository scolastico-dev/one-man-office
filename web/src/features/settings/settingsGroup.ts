// Several settings items edited on one page with one Save bar: the Roles page
// keeps one file per role, each with its own revision. Every item stays a
// `createSettingsResource` (loaded, kept current, saved with If-Match on its
// own); the group adds up what the bar shows and saves every changed item in
// order. An item refused or in conflict keeps its message under its own form
// (fieldErrors, error, conflict with useTheirs/keepMine); items saved before
// or after it stay saved. The group says "Saved." once, and in its error
// which items were not saved.
import { computed, getCurrentScope, onScopeDispose, ref, watch, type ComputedRef, type Ref } from 'vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useBoard } from '../../store'
import { createSettingsResource, type SaveOutcome, type SettingsResource, type SettingsResourceDeps, type SettingsResourceOptions } from './useSettingsResource'

export interface GroupMember<K extends string, T> {
  key: K
  /** The item in words for the bar's summary: "the developer". */
  name: string
  options: SettingsResourceOptions<T>
}

export interface SettingsGroup<K extends string, T> {
  /** Each item's resource, for its form. */
  members: Readonly<Record<K, SettingsResource<T>>>
  /** Some item has unsaved changes. */
  dirty: ComputedRef<boolean>
  /** The group's save is running. */
  saving: Readonly<Ref<boolean>>
  /** For the Save bar: `conflict` while an item waits for a choice, `saved` after a save that stored everything, until the next edit. */
  status: ComputedRef<'saved' | 'conflict' | null>
  /** Which items the last save did not store; empty when it stored all. */
  error: ComputedRef<string>
  /** Saves every item with unsaved changes (or a conflict), one after the other; never throws. */
  save(): Promise<Partial<Record<K, SaveOutcome>>>
  /** Drops every item's edits; an item in a conflict takes the stored value. */
  discard(): void
  dispose(): void
}

/** "a", "a and b", "a, b and c" */
function list(names: string[]): string {
  return names.length < 2 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}

/** The group with explicit dependencies, for tests. Call `dispose` when done. */
export function createSettingsGroup<K extends string, T>(entries: readonly GroupMember<K, T>[], deps: SettingsResourceDeps): SettingsGroup<K, T> {
  const announce = deps.announce ?? { polite: () => {}, assertive: () => {} }
  // Members announce their refusals and conflicts; "Saved." is the group's to say, once.
  const quiet = { polite: (t: string) => t !== 'Saved.' && announce.polite(t), assertive: (t: string) => announce.assertive(t) }
  const made = entries.map((e) => ({ ...e, r: createSettingsResource(e.options, { store: deps.store, announce: quiet }) }))
  const members = Object.fromEntries(made.map((m) => [m.key, m.r])) as Record<K, SettingsResource<T> & { dispose(): void }>

  const saving = ref(false)
  const failed = ref<{ saved: string[]; failed: string[] } | null>(null)
  const justSaved = ref(false)
  const dirty = computed(() => made.some((m) => m.r.dirty.value))
  const conflicting = computed(() => made.some((m) => m.r.conflict.value !== null))
  const status = computed(() => (conflicting.value ? 'conflict' : justSaved.value && !dirty.value ? 'saved' : null))
  const stopDirty = watch(dirty, (d) => {
    if (d) justSaved.value = false
    // Nothing left unsaved (a choice made, a discard): the summary is over.
    else if (!conflicting.value) failed.value = null
  })
  const error = computed(() => {
    const f = failed.value
    if (f === null || f.failed.length === 0) return ''
    const saved = f.saved.length > 0 ? `Saved ${list(f.saved)}. ` : ''
    return `${saved}Not saved: ${list(f.failed)}; see the message under ${f.failed.length === 1 ? 'it' : 'each'}.`
  })

  async function save(): Promise<Partial<Record<K, SaveOutcome>>> {
    if (saving.value) return {}
    saving.value = true
    failed.value = null
    justSaved.value = false
    const out: Partial<Record<K, SaveOutcome>> = {}
    try {
      for (const m of made) {
        if (!m.r.dirty.value && m.r.conflict.value === null) continue
        out[m.key] = await m.r.save()
      }
    } finally {
      saving.value = false
    }
    const done = made.filter((m) => out[m.key] === 'saved' || out[m.key] === 'unchanged')
    const notDone = made.filter((m) => m.key in out && !done.includes(m))
    if (notDone.length > 0) {
      failed.value = { saved: done.filter((m) => out[m.key] === 'saved').map((m) => m.name), failed: notDone.map((m) => m.name) }
    } else if (done.length > 0) {
      justSaved.value = true
      announce.polite('Saved.')
    }
    return out
  }

  function discard() {
    for (const m of made) m.r.discard()
    failed.value = null
  }

  return {
    members,
    dirty,
    saving,
    status,
    error,
    save,
    discard,
    dispose() {
      stopDirty()
      for (const m of made) m.r.dispose()
    },
  }
}

/**
 * The group for the calling component: events come from the app's board
 * store, refusals and conflicts are announced, and everything stops when the
 * component unmounts. The page puts each member's resource on its form and
 * one SaveBar for the group (with the leave guard on `dirty`).
 */
export function useSettingsGroup<K extends string, T>(entries: readonly GroupMember<K, T>[]): SettingsGroup<K, T> {
  const g = createSettingsGroup(entries, { store: useBoard(), announce: useAnnouncer() })
  if (getCurrentScope()) onScopeDispose(g.dispose)
  return g
}

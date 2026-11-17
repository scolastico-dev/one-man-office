// Which first steps the user dismissed, and whether the whole list is
// hidden: a per-browser convenience in local storage under `omo.firstSteps`.
// Without storage (blocked, private window) a dismissal lasts until the tab
// closes.
import { reactive } from 'vue'

export const FIRST_STEPS_KEY = 'omo.firstSteps'

export interface Dismissed {
  /** Ids of the steps the user dismissed. */
  steps: string[]
  /** The whole list is hidden. */
  hidden: boolean
}

function storage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

/** The stored choice; anything unreadable is nothing dismissed. */
export function readDismissed(s: Storage | null = storage()): Dismissed {
  let raw: unknown = null
  try {
    raw = JSON.parse(s?.getItem(FIRST_STEPS_KEY) ?? 'null')
  } catch {
    raw = null
  }
  const o = (typeof raw === 'object' && raw !== null ? raw : {}) as Record<string, unknown>
  return {
    steps: Array.isArray(o.steps) ? o.steps.filter((x): x is string => typeof x === 'string') : [],
    hidden: o.hidden === true,
  }
}

let shared: Dismissed | null = null

function save(d: Dismissed) {
  try {
    storage()?.setItem(FIRST_STEPS_KEY, JSON.stringify(d))
  } catch {
    // Storage full or blocked: the choice lasts until the tab closes.
  }
}

/** The dismissals of this tab, written back on change. */
export function useDismissed() {
  shared ??= reactive(readDismissed())
  const d = shared
  return {
    state: d as Readonly<Dismissed>,
    dismiss(id: string) {
      if (!d.steps.includes(id)) d.steps.push(id)
      save(d)
    },
    hideAll() {
      d.hidden = true
      save(d)
    },
  }
}

/** Forgets the shared state, so the next use reads storage again (tests). */
export function resetDismissed(): void {
  shared = null
}

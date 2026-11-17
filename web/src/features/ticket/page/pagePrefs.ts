// How this browser lays out a ticket's page: whether the details column
// shows beside the tabs, and the Diff tab's file list (shown or not, and its
// width). A per-browser convenience in local storage under `omo.ticketPage`;
// without storage (blocked, private window) the defaults apply and changes
// last until the tab closes.
import { effectScope, reactive, watch } from 'vue'

export const PAGE_PREFS_KEY = 'omo.ticketPage'

export const FILES_WIDTH = { min: 200, max: 640, initial: 300 } as const

export interface PagePrefs {
  /** The details column shows beside the tabs (on a wide page). */
  details: boolean
  /** The Diff tab's file list shows. */
  files: boolean
  /** Width of the Diff tab's file list, in px. */
  filesWidth: number
}

const DEFAULTS: PagePrefs = { details: true, files: true, filesWidth: FILES_WIDTH.initial }

function storage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

/** The stored choices, each checked; anything unreadable is the default. */
export function readPagePrefs(s: Storage | null = storage()): PagePrefs {
  let raw: unknown = null
  try {
    raw = JSON.parse(s?.getItem(PAGE_PREFS_KEY) ?? 'null')
  } catch {
    raw = null
  }
  const o = (typeof raw === 'object' && raw !== null ? raw : {}) as Record<string, unknown>
  const width = typeof o.filesWidth === 'number' && Number.isFinite(o.filesWidth) ? Math.round(o.filesWidth) : DEFAULTS.filesWidth
  return {
    details: typeof o.details === 'boolean' ? o.details : DEFAULTS.details,
    files: typeof o.files === 'boolean' ? o.files : DEFAULTS.files,
    filesWidth: Math.min(FILES_WIDTH.max, Math.max(FILES_WIDTH.min, width)),
  }
}

let shared: PagePrefs | null = null

/** The choices, shared by every ticket page of this tab and written back on change. */
export function usePagePrefs(): PagePrefs {
  if (shared) return shared
  const prefs = reactive(readPagePrefs())
  // Outlives the page that asked first: the next page uses the same choices.
  effectScope(true).run(() =>
    watch(prefs, (p) => {
      try {
        storage()?.setItem(PAGE_PREFS_KEY, JSON.stringify(p))
      } catch {
        // Storage refused (full or blocked): the choice lasts until the tab closes.
      }
    }),
  )
  shared = prefs
  return prefs
}

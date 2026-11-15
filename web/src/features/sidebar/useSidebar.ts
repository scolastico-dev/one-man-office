// The sidebar's state, shared by the whole app: open or collapsed, its width,
// the selected tab and what that tab shows. Open state, width and tab are a
// per-browser convenience kept in local storage; nothing depends on them, and
// every read and write is allowed to fail.
import { computed, readonly, ref, type ComputedRef, type Ref } from 'vue'

export const SIDEBAR_KEY = 'omo.sidebar'
export const REM = 16
export const DEFAULT_WIDTH = 30 * REM
export const MIN_WIDTH = 20 * REM
/** The widest the sidebar may be, as a share of the window. */
export const MAX_SHARE = 0.6
/** Below this width the sidebar is a full-screen sheet opened from the top bar. */
export const NARROW_QUERY = '(max-width: 767.98px)'

export type TabId = string
/** How a tab became selected: the user chose it, or it was read back from storage. */
export type SelectHow = 'user' | 'restored'

export interface Sidebar {
  /** The column is expanded (wide screens). */
  open: Readonly<Ref<boolean>>
  /** The sheet is shown (narrow screens). */
  sheet: Readonly<Ref<boolean>>
  /** The window is narrow: the sheet replaces the column. */
  narrow: Readonly<Ref<boolean>>
  /** The expanded width in px, before the window's limit applies. */
  width: Ref<number>
  tab: Readonly<Ref<TabId>>
  /** How the selected tab got selected; `restored` until the user picks one. */
  how: Readonly<Ref<SelectHow>>
  /** What the selected tab is asked to show (an agent's session id, a terminal's id); null for its list. */
  selection: Readonly<Ref<string | null>>
  /**
   * A ticket's panel covers the sidebar. Its terminals are unmounted then:
   * the panel can show the same terminal, and two views would fight over
   * its size.
   */
  covered: Readonly<Ref<boolean>>
  /** Counts up whenever a feature asked to open the sidebar, so it can move the focus in. */
  focusRequests: Readonly<Ref<number>>
  /** Whether the column or the sheet shows now. */
  shown: ComputedRef<boolean>
  setTab(id: TabId, how: SelectHow): void
  select(id: string | null): void
  /** Expands the column, or shows the sheet on a narrow window. */
  show(): void
  /** Collapses the column, or hides the sheet. */
  hide(): void
  toggle(): void
  /** Shows the sidebar on `tab`, optionally with something selected in it; moves the focus into it. */
  openOn(tab: TabId, select?: string | null): void
  /** Called by the window's size watcher. */
  setNarrow(narrow: boolean): void
  /** Called by the sidebar while a ticket's panel covers it. */
  setCovered(covered: boolean): void
}

interface Stored {
  open?: unknown
  width?: unknown
  tab?: unknown
}

function getStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

function load(storage: () => Storage | null): { open: boolean; width: number; tab: TabId } {
  const out = { open: false, width: DEFAULT_WIDTH, tab: 'agents' }
  try {
    const raw = storage()?.getItem(SIDEBAR_KEY)
    if (!raw) return out
    const v = JSON.parse(raw) as Stored | null
    if (v === null || typeof v !== 'object') return out
    if (typeof v.open === 'boolean') out.open = v.open
    if (typeof v.width === 'number' && Number.isFinite(v.width) && v.width >= MIN_WIDTH && v.width <= 4096) out.width = Math.round(v.width)
    if (typeof v.tab === 'string' && /^[a-z]{1,16}$/.test(v.tab)) out.tab = v.tab
  } catch {
    // Unreadable storage or text: the defaults.
  }
  return out
}

/** One sidebar's state; `useSidebar` shares a single one. Exported for tests. */
export function createSidebar(storage: () => Storage | null = getStorage): Sidebar {
  const saved = load(storage)
  const open = ref(saved.open)
  const sheet = ref(false)
  const narrow = ref(false)
  const width = ref(saved.width)
  const tab = ref<TabId>(saved.tab)
  const how = ref<SelectHow>('restored')
  const selection = ref<string | null>(null)
  const focusRequests = ref(0)
  const covered = ref(false)

  function save() {
    try {
      storage()?.setItem(SIDEBAR_KEY, JSON.stringify({ open: open.value, width: width.value, tab: tab.value }))
    } catch {
      // Storage is full or refused: the state lasts until the page closes.
    }
  }

  const sidebar: Sidebar = {
    open: readonly(open),
    sheet: readonly(sheet),
    narrow: readonly(narrow),
    width: computed({
      get: () => width.value,
      set: (v: number) => {
        width.value = Math.round(v)
        save()
      },
    }) as Ref<number>,
    tab: readonly(tab),
    how: readonly(how),
    selection: readonly(selection),
    focusRequests: readonly(focusRequests),
    covered: readonly(covered),
    shown: computed(() => (narrow.value ? sheet.value : open.value)),
    setTab(id, h) {
      if (id !== tab.value) selection.value = null
      tab.value = id
      how.value = h
      if (h === 'user') save()
    },
    select(id) {
      selection.value = id
    },
    show() {
      if (narrow.value) sheet.value = true
      else {
        open.value = true
        save()
      }
    },
    hide() {
      if (narrow.value) sheet.value = false
      else {
        open.value = false
        save()
      }
    },
    toggle() {
      if (sidebar.shown.value) sidebar.hide()
      else sidebar.show()
    },
    openOn(id, select = null) {
      sidebar.setTab(id, 'user')
      selection.value = select
      sidebar.show()
      focusRequests.value++
    },
    setNarrow(n) {
      narrow.value = n
      // The sheet is for the moment only: a window that grows back shows the column.
      if (!n) sheet.value = false
    },
    setCovered(c) {
      covered.value = c
    },
  }
  return sidebar
}

let shared: Sidebar | null = null

/** The app's sidebar. */
export function useSidebar(): Sidebar {
  shared ??= createSidebar()
  return shared
}

/** Forgets the shared sidebar, so the next `useSidebar` reads storage again (tests). */
export function resetSidebar(): void {
  shared = null
}

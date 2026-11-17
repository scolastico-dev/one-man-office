import { computed, effectScope, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { useRouter, type LocationQuery, type Router } from 'vue-router'
import { listTickets } from '../../api/endpoints'
import type { ApiClient } from '../../api/client'
import type { Role, TicketSummary } from '../../api/types'
import { matches, type Filters } from '../../board/filters'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'

export type { Filters }

/** The supervisors the filter offers. */
export const SUPERVISOR_FILTERS: ReadonlyArray<{ role: Role; label: string }> = [
  { role: 'user', label: 'You' },
  { role: 'ceo', label: 'CEO' },
  { role: 'pm', label: 'Product manager' },
]

/** How long typing must pause before the server is asked. */
export const SEARCH_DEBOUNCE_MS = 250
/** How often the same search is repeated when an event made part of its answer stale. */
const SEARCH_RETRIES = 2

export interface FilterState {
  /**
   * The active filters. Assigning a new object changes them; components never
   * mutate the object in place.
   */
  filters: Ref<Filters>
  /**
   * Tickets the server found for the search text; undefined while no search
   * answered for the text that is typed now.
   */
  serverHits: Ref<Set<number> | undefined>
  /**
   * The server's hits the board has not loaded (old Done tickets beyond the loaded pages). They are
   * kept here, never in the store, so a search leaves no gap in Done; they last until the text changes.
   */
  searchOnly: Ref<readonly TicketSummary[]>
  /** The server is being asked about the typed text. */
  searching: Ref<boolean>
  /** The server could not search; the board shows the matches of numbers and titles only. */
  searchFailed: Ref<boolean>
  /** Tickets that need the user among those that pass the other filters. */
  needsMeCount: ComputedRef<number>
  /** How many filters are set. */
  activeCount: ComputedRef<number>
  /** Removes every filter. */
  clear(): void
}

const QUERY_KEYS = ['project', 'story', 'supervisor', 'needs_me', 'q'] as const

function first(v: LocationQuery[string] | undefined): string | undefined {
  const s = Array.isArray(v) ? v[0] : v
  return typeof s === 'string' && s !== '' ? s : undefined
}

/** The filters an address query says; anything malformed is ignored. */
export function filtersFromQuery(query: LocationQuery): Filters {
  const f: Filters = {}
  const project = first(query.project)
  if (project !== undefined) f.project = project
  const story = first(query.story)
  if (story !== undefined && /^[1-9]\d*$/.test(story)) f.story = Number(story)
  const sup = first(query.supervisor)
  if (sup !== undefined && SUPERVISOR_FILTERS.some((s) => s.role === sup)) f.supervisor = sup as Role
  if (first(query.needs_me) === '1') f.needsMe = true
  const q = first(query.q)
  if (q !== undefined && q.trim() !== '') f.q = q
  return f
}

/** The query parameters for the filters; only set filters appear. */
export function filtersToQuery(f: Filters): Record<string, string> {
  const q: Record<string, string> = {}
  if (f.project !== undefined && f.project !== '') q.project = f.project
  if (f.story !== undefined) q.story = String(f.story)
  if (f.supervisor !== undefined) q.supervisor = f.supervisor
  if (f.needsMe === true) q.needs_me = '1'
  if (f.q !== undefined && f.q.trim() !== '') q.q = f.q
  return q
}

const keyOf = (f: Filters) => JSON.stringify(filtersToQuery(f))

function create(router: Router, store: BoardStore, client: ApiClient): FilterState {
  // The state outlives the component that asked first, so it has a scope of its own.
  return effectScope(true).run(() => {
    const state = ref<Filters>(filtersFromQuery(router.currentRoute.value.query))
    const serverHits = ref<Set<number> | undefined>(undefined)
    const searchOnly = shallowRef<readonly TicketSummary[]>([])
    const searching = ref(false)
    const searchFailed = ref(false)

    // Keys written to the address that the router has not reported back yet, so
    // a late report of an older write does not undo a newer change.
    const written: string[] = []
    watch(
      () => router.currentRoute.value.query,
      (query) => {
        const f = filtersFromQuery(query)
        const k = keyOf(f)
        const i = written.indexOf(k)
        if (i >= 0) {
          written.splice(0, i + 1)
          return
        }
        written.length = 0
        if (k !== keyOf(state.value)) state.value = f
      },
    )

    const filters = computed<Filters>({
      get: () => state.value,
      set(f) {
        const next = filtersFromQuery(filtersToQuery(f))
        if (keyOf(next) === keyOf(state.value)) return
        state.value = next
        const current = router.currentRoute.value
        const query: LocationQuery = {}
        for (const [k, v] of Object.entries(current.query)) if (!(QUERY_KEYS as readonly string[]).includes(k)) query[k] = v
        Object.assign(query, filtersToQuery(next))
        written.push(keyOf(next))
        void router.replace({ query })
      },
    })

    // Search: the text is matched in the browser at once; the server is asked
    // after a pause, and only the answer to the text typed now is ever used.
    let seq = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    async function ask(q: string, mine: number): Promise<void> {
      try {
        for (let attempt = 0; ; attempt++) {
          const list = (await listTickets(client, { q, done_limit: 500 })).data
          if (mine !== seq) return
          // Tickets the store has are shown from the store, which the stream keeps current; the others stay out of it.
          const unknown = list.tickets.filter((t) => store.view(t.number) === undefined)
          // An event newer than this answer was left out for a ticket the store lacks: the answer is old for it.
          const stale = new Set(unknown.filter((t) => (store.state.skipped.get(t.number) ?? 0) > list.event_id).map((t) => t.number))
          if (stale.size > 0 && attempt < SEARCH_RETRIES) continue
          searchOnly.value = unknown.filter((t) => !stale.has(t.number))
          serverHits.value = new Set(list.tickets.map((t) => t.number))
          searching.value = false
          return
        }
      } catch {
        if (mine !== seq) return
        searchFailed.value = true
        searching.value = false
      }
    }
    watch(
      () => (state.value.q ?? '').trim(),
      (q) => {
        const mine = ++seq
        clearTimeout(timer)
        serverHits.value = undefined
        searchOnly.value = []
        searchFailed.value = false
        searching.value = q !== ''
        if (q !== '') timer = setTimeout(() => void ask(q, mine), SEARCH_DEBOUNCE_MS)
      },
      { immediate: true },
    )

    const needsMeCount = computed(() => {
      const f: Filters = { ...state.value, needsMe: true }
      let n = 0
      for (const e of store.state.tickets.values()) if (matches(e.t, f, serverHits.value)) n++
      return n
    })
    const activeCount = computed(() => Object.keys(filtersToQuery(state.value)).length)

    return {
      filters,
      serverHits,
      searchOnly,
      searching,
      searchFailed,
      needsMeCount,
      activeCount,
      clear() {
        filters.value = {}
      },
    }
  })!
}

const shared = new WeakMap<Router, FilterState>()

/**
 * The board's filters: project, story, supervisor, "needs me" and search.
 * They live in the address query (`project`, `story`, `supervisor`,
 * `needs_me=1`, `q`), so a reload or a shared link keeps them. Every caller
 * gets the same state, so the board and the filter bar always agree.
 */
export function useFilters(): FilterState {
  const router = useRouter()
  let s = shared.get(router)
  if (!s) {
    const session = useSession()
    s = create(router, session.store, session.client)
    shared.set(router, s)
  }
  return s
}

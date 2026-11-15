import { computed, ref, type Ref } from 'vue'
import type { Filters } from '../../board/filters'

export type { Filters }

export interface FilterState {
  /**
   * The active filters. Assigning a new object changes them; components never
   * mutate the object in place.
   */
  filters: Ref<Filters>
  /** Tickets the server found for the search text; undefined while no search ran. */
  serverHits: Ref<Set<number> | undefined>
}

/**
 * The board's filters: project, story, supervisor, "needs me" and search.
 *
 * To be filled in: keep them in the route query (`project`, `story`,
 * `supervisor`, `needs_me=1`, `q`) with `router.replace`, so reloads and
 * shared links keep them; set `serverHits` from a 250 ms debounced
 * `listTickets({ q, done_limit: 500 })` whose answer is merged into the store
 * with `mergeTickets`, a newer query cancelling an older request. Several
 * components call this (the board and the filter bar), so the search state
 * must be shared, not created per call.
 *
 * For now no filter is ever set and assignments are ignored.
 */
export function useFilters(): FilterState {
  const filters = computed<Filters>({
    get: () => ({}),
    set: () => {},
  })
  return { filters, serverHits: ref(undefined) }
}

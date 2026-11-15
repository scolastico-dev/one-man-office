import { computed, type ComputedRef } from 'vue'
import type { TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { badgeFor, captionFor } from '../../board/badges'
import { matches } from '../../board/filters'
import { useBoard, type BoardStore } from '../../store'
import { useFilters, type FilterState } from '../filters/useFilters'
import { COLUMN_TITLES, type CardModel, type ColumnModel } from './model'

export interface BoardColumnsOptions {
  /** The store; `useBoard()` by default. */
  store?: BoardStore
  /** The filters; `useFilters()` by default. */
  filters?: FilterState
}

interface Cached {
  ticket: TicketSummary
  storyTitle: string | undefined
  projectNames: string[]
  pending: boolean
  model: CardModel
}

function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i])
}

/**
 * The five columns as the board shows them, from the store alone: its
 * ordered columns with pending moves applied, filtered, each ticket turned
 * into a card. A card's model is kept while its ticket, story title, project
 * names and pending state stay the same, so a change to one ticket rebuilds
 * one card and the other cards do not render again.
 */
export function useBoardColumns(o: BoardColumnsOptions = {}): ComputedRef<ColumnModel[]> {
  const store = o.store ?? useBoard()
  const { filters, serverHits } = o.filters ?? useFilters()
  const cache = new Map<number, Cached>()

  function card(t: TicketSummary, seen: Set<number>): CardModel {
    seen.add(t.number)
    const storyTitle = t.story === null ? undefined : store.view(t.story)?.title
    const projectNames = t.projects.map((p) => store.state.projects.get(p.project)?.name ?? p.project)
    const pending = store.pending.has(t.number)
    const c = cache.get(t.number)
    if (c && c.ticket === t && c.storyTitle === storyTitle && c.pending === pending && sameList(c.projectNames, projectNames)) {
      return c.model
    }
    const model: CardModel = {
      ticket: t,
      storyTitle,
      projectNames,
      badge: badgeFor(t),
      caption: captionFor(t),
      pending,
      refusedFlash: false,
    }
    cache.set(t.number, { ticket: t, storyTitle, projectNames, pending, model })
    return model
  }

  return computed(() => {
    const f = filters.value
    const hits = serverHits.value
    const shown = (t: TicketSummary) => matches(t, f, hits)
    const all = store.columns.value
    const seen = new Set<number>()
    const out = COLUMNS.map((column): ColumnModel => {
      const col = all[column]
      const base = { column, title: COLUMN_TITLES[column], dropState: 'idle' as const }
      if (column === 'done') {
        const cards = col.all.filter(shown).map((t) => card(t, seen))
        return { ...base, cards, count: cards.length, more: store.state.doneBefore !== null }
      }
      const stories = col.stories.filter(shown).map((t) => card(t, seen))
      const tasks = col.tasks.filter(shown).map((t) => card(t, seen))
      return { ...base, cards: [...stories, ...tasks], groups: { stories, tasks }, count: stories.length + tasks.length }
    })
    for (const n of cache.keys()) if (!seen.has(n)) cache.delete(n)
    return out
  })
}

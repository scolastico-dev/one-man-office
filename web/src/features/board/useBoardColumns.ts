import { computed, type ComputedRef, type Ref } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { badgeFor, captionFor } from '../../board/badges'
import { matches } from '../../board/filters'
import { useBoard, type BoardStore } from '../../store'
import { useFilters, type FilterState } from '../filters/useFilters'
import type { Target } from '../../board/moveRules'
import { COLUMN_TITLES, type CardModel, type CardNotice, type ColumnModel } from './model'
import type { DragActive } from './useDrag'
import { dropHint, placesCard } from './useMoves'

export interface BoardColumnsOptions {
  /** The store; `useBoard()` by default. */
  store?: BoardStore
  /** The filters; `useFilters()` by default. */
  filters?: Pick<FilterState, 'filters' | 'serverHits'>
  /** The drag under way: the columns show which accept the card and what a drop there does. */
  drag?: { active: Readonly<Ref<DragActive | null>>; targets(n: number): Record<Column, Target> }
  /** How recent moves ended: a refused card flashes, and a card may carry a notice. */
  moves?: { refused(n: number): boolean; notice(n: number): CardNotice | undefined }
}

interface Cached {
  ticket: TicketSummary
  storyTitle: string | undefined
  projectNames: string[]
  pending: boolean
  refused: boolean
  notice: CardNotice | undefined
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
    const refused = o.moves?.refused(t.number) ?? false
    const notice = o.moves?.notice(t.number)
    const c = cache.get(t.number)
    if (
      c &&
      c.ticket === t &&
      c.storyTitle === storyTitle &&
      c.pending === pending &&
      c.refused === refused &&
      c.notice === notice &&
      sameList(c.projectNames, projectNames)
    ) {
      return c.model
    }
    const model: CardModel = {
      ticket: t,
      storyTitle,
      projectNames,
      badge: badgeFor(t),
      caption: captionFor(t),
      pending,
      refusedFlash: refused,
      ...(notice ? { notice } : {}),
    }
    cache.set(t.number, { ticket: t, storyTitle, projectNames, pending, refused, notice, model })
    return model
  }

  return computed(() => {
    const f = filters.value
    const hits = serverHits.value
    const shown = (t: TicketSummary) => matches(t, f, hits)
    const all = store.columns.value
    const seen = new Set<number>()
    const drag = o.drag?.active.value ?? null
    const targets = drag === null ? null : o.drag!.targets(drag.n)
    const dragged = drag === null ? undefined : store.view(drag.n)
    const out = COLUMNS.map((column): ColumnModel => {
      const col = all[column]
      const base: Pick<ColumnModel, 'column' | 'title' | 'dropState' | 'reason' | 'action' | 'marker'> = { column, title: COLUMN_TITLES[column], dropState: 'idle' }
      const target = targets?.[column]
      if (drag !== null && target !== undefined) {
        if (!target.allowed) {
          base.dropState = 'refused'
          base.reason = target.reason
        } else {
          base.dropState = drag.over === column ? 'over' : 'allowed'
          base.action = dropHint(target)
          if (drag.over === column && dragged !== undefined && placesCard(target)) base.marker = { type: dragged.type, before: drag.marker }
        }
      }
      if (column === 'done') {
        const cards = col.all.filter(shown).map((t) => card(t, seen))
        return { ...base, cards, count: cards.length, total: col.all.length, more: store.state.doneBefore !== null }
      }
      const stories = col.stories.filter(shown).map((t) => card(t, seen))
      const tasks = col.tasks.filter(shown).map((t) => card(t, seen))
      return { ...base, cards: [...stories, ...tasks], groups: { stories, tasks }, count: stories.length + tasks.length, total: col.all.length }
    })
    for (const n of cache.keys()) if (!seen.has(n)) cache.delete(n)
    return out
  })
}

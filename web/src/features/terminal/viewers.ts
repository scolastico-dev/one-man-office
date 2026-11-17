// One live view per terminal in a window.
//
// Two views of one terminal fight over its size (each fits the terminal to
// its own box and the last resize wins), so within a window only one view of
// a terminal is live; the others show a placeholder that says where it is
// and holds no connection. The view opened last is the live one: the user
// just asked to see the terminal there (the panel's Agents tab opened next to
// the sidebar showing the same agent). When it goes away, the view before it
// is live again; a placeholder's "Show it here" makes its own view the live one.
// A terminal open in its own window is the popouts' business (`popout.ts`).

import { shallowRef, type Ref } from 'vue'

export interface Viewer {
  id: number
  /** Where the view is, in words for the others' placeholder ("the ticket's panel"). */
  place: string
}

export interface TerminalViewers {
  /** Every terminal's views by key, the live one last. */
  readonly views: Readonly<Ref<ReadonlyMap<string, readonly Viewer[]>>>
  /** Adds a view of terminal `key`; it is the live one. Returns its id. */
  claim(key: string, place: string): number
  /** Names the place of a view once it is known (after it is mounted). */
  setPlace(key: string, id: number, place: string): void
  /** Removes a view; the one before it is live again. */
  release(key: string, id: number): void
  /** Makes view `id` the live one. */
  takeOver(key: string, id: number): void
  /** The live view of terminal `key`, or null. */
  live(key: string): Viewer | null
}

export function createTerminalViewers(): TerminalViewers {
  const views = shallowRef<ReadonlyMap<string, readonly Viewer[]>>(new Map())
  let next = 0

  function update(key: string, f: (list: readonly Viewer[]) => Viewer[]) {
    const map = new Map(views.value)
    const list = f(map.get(key) ?? [])
    if (list.length === 0) map.delete(key)
    else map.set(key, list)
    views.value = map
  }

  return {
    views,
    claim(key, place) {
      const id = ++next
      update(key, (l) => [...l, { id, place }])
      return id
    },
    setPlace(key, id, place) {
      const v = views.value.get(key)?.find((x) => x.id === id)
      if (!v || v.place === place) return
      update(key, (l) => l.map((x) => (x.id === id ? { id, place } : x)))
    },
    release(key, id) {
      if (!views.value.get(key)?.some((x) => x.id === id)) return
      update(key, (l) => l.filter((x) => x.id !== id))
    },
    takeOver(key, id) {
      const v = views.value.get(key)?.find((x) => x.id === id)
      if (!v) return
      update(key, (l) => [...l.filter((x) => x.id !== id), v])
    },
    live(key) {
      const l = views.value.get(key)
      return l?.[l.length - 1] ?? null
    },
  }
}

let shared: TerminalViewers | null = null

/** This window's terminal views. */
export function useTerminalViewers(): TerminalViewers {
  shared ??= createTerminalViewers()
  return shared
}

/** Forgets every view (tests). */
export function resetTerminalViewers(): void {
  shared = null
}

/** Where an element is, in words for a placeholder elsewhere. */
export function placeOf(el: Element | null): string {
  if (el?.closest('[data-ticket-panel]')) return "the ticket's panel"
  if (el?.closest('[data-ticket-page]')) return "the ticket's page"
  if (el?.closest('[data-sidebar], .side-sheet')) return 'the sidebar'
  return 'another place in this window'
}

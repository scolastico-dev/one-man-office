// Moving cards with the keyboard. A focused card is picked up with Space or
// `m`, carried from column to column with the left and right arrows (columns
// that refuse it are skipped) and within its group with up and down, then
// dropped with Space or Enter, or put back with Escape. What a drop means
// comes from `targets`, and the drop itself goes to `drop`, as for dragging.
import { computed, readonly, shallowRef, type Ref } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import type { Target } from '../../board/moveRules'
import { beforeFor } from '../../board/ordering'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { COLUMN_TITLES } from './model'
import type { DragActive } from './useDrag'
import { actionWords, placesCard, type CancelReason, type MoveOptions } from './useMoves'

/** The card being moved and where it would land. */
export interface Picked {
  n: number
  /** The column it would be dropped on. */
  column: Column
  /** Its place among the other cards of its own group in `column`, 0 the top. */
  index: number
}

export interface KeyboardMoveOptions extends MoveOptions {
  /** Says that a move ended without a request; the same words as for dragging. */
  cancelled?(n: number, why: CancelReason, target?: Target): void
  announcer?: { polite(text: string): void }
}

export interface KeyboardMove {
  picked: Readonly<Ref<Picked | null>>
  /** The move as the columns show it, in the shape of a drag. */
  active: Readonly<Ref<DragActive | null>>
  /** Keys pressed on the card of ticket `n`. */
  onKeydown(e: KeyboardEvent, n: number): void
  /** Focus left the card of ticket `n`: a move of it ends. */
  onFocusOut(e: FocusEvent, n: number): void
  /** Ends the move without a drop. */
  cancel(why?: CancelReason): void
}

const HELP = 'Arrow keys move, Space drops, Escape cancels.'

export function useKeyboardMove(o: KeyboardMoveOptions): KeyboardMove {
  const say = o.announcer ?? useAnnouncer()
  const picked = shallowRef<Picked | null>(null)

  function ticket(n: number): TicketSummary | undefined {
    for (const c of COLUMNS) {
      const t = o.order(c).find((x) => x.number === n)
      if (t !== undefined) return t
    }
    return undefined
  }

  /** The other cards of `t`'s group in `column`, in display order. */
  function group(column: Column, t: TicketSummary): TicketSummary[] {
    return o.order(column).filter((x) => x.type === t.type && x.number !== t.number)
  }

  /** Where `t` is now among the other cards of its group. */
  function ownIndex(t: TicketSummary): number {
    const all = o.order(t.column).filter((x) => x.type === t.type)
    return Math.max(0, all.findIndex((x) => x.number === t.number))
  }

  function before(p: Picked, t: TicketSummary): number | null {
    return beforeFor(o.order(p.column), t, p.index, o.unanswered)
  }

  const active = computed((): DragActive | null => {
    const p = picked.value
    if (p === null) return null
    const t = ticket(p.n)
    const target = o.targets(p.n)[p.column]
    if (t === undefined || !placesCard(target)) return { n: p.n, over: p.column, index: p.index, before: null, marker: null }
    return { n: p.n, over: p.column, index: p.index, before: before(p, t), marker: group(p.column, t)[p.index]?.number ?? null }
  })

  function position(p: Picked, t: TicketSummary): string {
    return `Position ${p.index + 1} of ${group(p.column, t).length + 1}.`
  }

  /** Columns and why they refuse the card, with one reason said once for all the columns that share it. */
  function reasons(refused: Target[]): string {
    const byReason = new Map<string, string[]>()
    for (const t of refused) {
      const r = t.reason ?? 'It cannot go there.'
      byReason.set(r, [...(byReason.get(r) ?? []), COLUMN_TITLES[t.column]])
    }
    return [...byReason].map(([r, cols]) => `${cols.join(', ')}: ${r}`).join(' ')
  }

  function pickUp(n: number): void {
    const t = ticket(n)
    if (t === undefined) return
    const p = { n, column: t.column, index: ownIndex(t) }
    picked.value = p
    const all = o.targets(n)
    const ok = COLUMNS.filter((c) => all[c].allowed).map((c) => `${COLUMN_TITLES[c]} (${actionWords(all[c])})`)
    const where = ok.length === 0 ? `No column accepts it. ${reasons([all[t.column]])}` : `It can go to ${ok.join(', ')}.`
    say.polite(`Picked up #${n}, ${t.title}. In ${COLUMN_TITLES[t.column]}, ${position(p, t).toLowerCase()} ${where} ${HELP}`)
  }

  function step(p: Picked, t: TicketSummary, dir: 1 | -1): void {
    const all = o.targets(p.n)
    const skipped: Target[] = []
    let next: Column | undefined
    for (let i = COLUMNS.indexOf(p.column) + dir; i >= 0 && i < COLUMNS.length; i += dir) {
      const c = COLUMNS[i]!
      if (all[c].allowed) {
        next = c
        break
      }
      skipped.push(all[c])
    }
    if (next === undefined) {
      const elsewhere = COLUMNS.some((c) => c !== p.column && all[c].allowed)
      const head = elsewhere ? `No column ${dir > 0 ? 'to the right' : 'to the left'} accepts #${p.n}.` : `No other column accepts #${p.n}.`
      say.polite(skipped.length === 0 ? head : `${head} ${reasons(skipped)}`)
      return
    }
    const target = all[next]
    const moved: Picked = { n: p.n, column: next, index: next === t.column ? ownIndex(t) : group(next, t).length }
    picked.value = moved
    const place = placesCard(target) ? ` ${position(moved, t)}` : ''
    say.polite(`${COLUMN_TITLES[next]}: ${actionWords(target)}.${place}`)
  }

  function shift(p: Picked, t: TicketSummary, dir: 1 | -1): void {
    const target = o.targets(p.n)[p.column]
    const title = COLUMN_TITLES[p.column]
    if (!target.allowed) {
      say.polite(`${title}: ${target.reason ?? 'It cannot go there.'}`)
      return
    }
    if (!placesCard(target)) {
      say.polite(`The place in ${title} cannot be chosen.`)
      return
    }
    const last = group(p.column, t).length
    const index = p.index + dir
    if (index < 0) {
      say.polite(`#${p.n} is already at the top.`)
      return
    }
    if (index > last) {
      say.polite(`#${p.n} is already at the bottom.`)
      return
    }
    const moved = { ...p, index }
    picked.value = moved
    say.polite(position(moved, t))
  }

  function drop(p: Picked, t: TicketSummary): void {
    const target = o.targets(p.n)[p.column]
    const b = placesCard(target) ? before(p, t) : null
    picked.value = null
    o.drop(p.n, target, b)
  }

  function cancel(why: CancelReason = 'escape'): void {
    const p = picked.value
    if (p === null) return
    picked.value = null
    o.cancelled?.(p.n, why)
  }

  function onKeydown(e: KeyboardEvent, n: number): void {
    if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
    const p = picked.value
    if (p === null || p.n !== n) {
      if (e.key !== ' ' && e.key !== 'm' && e.key !== 'M') return
      e.preventDefault()
      e.stopPropagation()
      if (p !== null) cancel('blur')
      pickUp(n)
      return
    }
    const t = ticket(n)
    if (t === undefined) {
      cancel('gone')
      return
    }
    switch (e.key) {
      case 'ArrowLeft':
        step(p, t, -1)
        break
      case 'ArrowRight':
        step(p, t, 1)
        break
      case 'ArrowUp':
        shift(p, t, -1)
        break
      case 'ArrowDown':
        shift(p, t, 1)
        break
      case ' ':
      case 'Enter':
        drop(p, t)
        break
      case 'Escape':
        cancel('escape')
        break
      default:
        return
    }
    e.preventDefault()
    e.stopPropagation()
  }

  function onFocusOut(e: FocusEvent, n: number): void {
    if (picked.value?.n !== n) return
    // No new focus: the card was drawn again, or the window lost focus; the move goes on.
    const to = e.relatedTarget
    if (!(to instanceof Element)) return
    if (to.closest('li[data-ticket]')?.getAttribute('data-ticket') === String(n)) return
    cancel('blur')
  }

  return {
    picked: readonly(picked) as Readonly<Ref<Picked | null>>,
    active,
    onKeydown,
    onFocusOut,
    cancel,
  }
}

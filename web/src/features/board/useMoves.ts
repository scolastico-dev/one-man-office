// What a move of a card means and how it ends, shared by every way of moving a
// card (dragging, the keyboard, the move menu): the targets come from the
// rules, the move itself from the store; this file asks first where a move
// needs it (reject, stop), and tells the user how each move ended.
import { inject, provide, readonly, ref, shallowReactive, shallowRef, watch, type InjectionKey, type Ref } from 'vue'
import type { ApiEvent, Column, Role, TicketGateData, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useToast } from '../../components/ui/useToast'
import { dragTargets, type RuleContext, type Target } from '../../board/moveRules'
import { holdNotice } from '../../board/gates'
import { beforeFor } from '../../board/ordering'
import type { BoardStore, MoveOutcome } from '../../store'
import { COLUMN_TITLES, type CardNotice } from './model'

/** What a way of moving cards needs: the targets of a card, the cards of a column, and the drop. */
export interface MoveOptions {
  /** What dropping card `n` on each column does. */
  targets(n: number): Record<Column, Target>
  /** Every card of a column in display order, filters ignored. */
  order(column: Column): TicketSummary[]
  /** Card `n` has a move of its own that omo has not answered yet; it is never named as the card another goes before. */
  unanswered?(n: number): boolean
  /** Moves card `n` to `target`, above `before` (null: the end of its group); asks first where needed. */
  drop(n: number, target: Target, before: number | null): void
}

/** Why a move ended without a request. */
export type CancelReason = 'escape' | 'outside' | 'refused' | 'blur' | 'pointer' | 'gone'

export type { CardNotice }

export interface StopAgent {
  ticket: number
  role: Role
  name: string
}

/** The dialog a drop opened, if any. */
export type MoveDialog =
  | { kind: 'reject'; ticket: TicketSummary; target: Target }
  | { kind: 'stop'; ticket: TicketSummary; to: 'planning' | 'ready'; agents: StopAgent[]; target: Target; before: number | null }

export interface Moves extends MoveOptions {
  /** The open dialog of a drop that asks first. */
  dialog: Readonly<Ref<MoveDialog | null>>
  /** The dialog's move was sent and has no answer yet. */
  dialogBusy: Readonly<Ref<boolean>>
  /** Why the server refused the dialog's move. */
  dialogError: Readonly<Ref<string>>
  /** Sends the dialog's move: the comment for a rejection, nothing for a stop. */
  confirmDialog(comment?: string): void
  /** Closes the dialog; nothing is sent. */
  closeDialog(): void
  /** The server refused a move of `n` within the last two seconds. */
  refused(n: number): boolean
  notice(n: number): CardNotice | undefined
  /** Announces that card `n` was picked up and where it may go. */
  pickedUp(n: number): void
  /** Announces that a move of `n` ended without a request. */
  cancelled(n: number, why: CancelReason, target?: Target): void
  /**
   * Ends a move in progress (`cancel`) when its card is moved or removed by
   * someone else meanwhile, and says so.
   */
  follow(moving: Readonly<Ref<{ n: number } | null>>, cancel: () => void): void
}

/** How long a refused card's outline flashes. */
export const REFUSED_FLASH_MS = 2000
/** How long a card shows the server's refusal or the lost answer. */
export const NOTICE_MS = 10_000

/** A move in a few words, for announcements: `approves`, `stops the agent`. */
export function actionWords(t: Target): string {
  switch (t.action) {
    case 'move':
      return 'marks as merged'
    case 'reorder':
      return 'changes priority'
    case 'approve':
      return t.resultColumn === 'review' ? 'approves the review' : 'approves'
    case 'withdraw':
      return 'withdraws approval'
    case 'stop':
      return 'stops the agent'
    case 'reject':
      return 'rejects'
    default:
      return ''
  }
}

/** What a column that accepts the dragged card says it will do. */
export function dropHint(t: Target): string {
  switch (t.action) {
    case 'move':
      return 'Drop to mark the export as merged and complete the ticket.'
    case 'reorder':
      return 'Drop to change the priority.'
    case 'approve':
      return t.resultColumn === 'review' ? 'Drop to approve the review.' : 'Drop to approve.'
    case 'withdraw':
      return 'Drop to withdraw the approval.'
    case 'stop':
      return 'Drop to stop the agent. You confirm first.'
    case 'reject':
      return 'Drop to reject. You write a comment first.'
    default:
      return ''
  }
}

/** Whether a drop on `t` takes a place among the column's cards. */
export function placesCard(t: Target): boolean {
  return t.allowed && t.action !== 'reject' && t.resultColumn === t.column && t.column !== 'done'
}

const GONE = 'This ticket is no longer on the board.'

export interface MovesDeps {
  store: BoardStore
  toast?: ReturnType<typeof useToast>
  announcer?: ReturnType<typeof useAnnouncer>
  timers?: { setTimeout(fn: () => void, ms: number): unknown; clearTimeout(h: unknown): void }
}

function message(e: unknown): string {
  return e instanceof Error && e.message !== '' ? e.message : 'omo refused the move.'
}

export function createMoves(deps: MovesDeps): Moves {
  const { store } = deps
  const toast = deps.toast ?? useToast()
  const say = deps.announcer ?? useAnnouncer()
  const timers = deps.timers ?? {
    setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms),
    clearTimeout: (h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>),
  }

  const dialog = shallowRef<MoveDialog | null>(null)
  const dialogBusy = ref(false)
  const dialogError = ref('')
  const flashes = shallowReactive(new Map<number, unknown>())
  const notices = shallowReactive(new Map<number, { notice: CardNotice; timer: unknown }>())

  const ctx: RuleContext = {
    project: (id) => store.state.projects.get(id),
    storyHasLiveAgent: (story) => store.tasksOf(story).some((t) => t.agents.length > 0),
  }

  function targets(n: number): Record<Column, Target> {
    const t = store.view(n)
    if (t !== undefined) return dragTargets(t, ctx)
    const out = {} as Record<Column, Target>
    for (const c of COLUMNS) out[c] = { column: c, allowed: false, reason: GONE }
    return out
  }

  function order(column: Column): TicketSummary[] {
    return store.columns.value[column].all
  }

  /** A move of `n` is shown but omo has not answered it (waiting in line, on its way, or its answer was lost). */
  function unanswered(n: number): boolean {
    return store.pending.get(n)?.waitFor === null
  }

  /** The `before` that keeps `t` where it is. */
  function currentBefore(t: TicketSummary): number | null {
    const all = order(t.column)
    const index = all.filter((x) => x.type === t.type && x.number !== t.number && (x.rank < t.rank || (x.rank === t.rank && x.number < t.number))).length
    return beforeFor(all, t, index, unanswered)
  }

  function setNotice(n: number, notice: CardNotice): void {
    const old = notices.get(n)
    if (old) timers.clearTimeout(old.timer)
    const timer = timers.setTimeout(() => {
      if (notices.get(n)?.timer === timer) notices.delete(n)
    }, NOTICE_MS)
    notices.set(n, { notice, timer })
  }

  // A plugin's answer about a move arrives on its own time, also for a move omo makes by itself.
  const gateData = (ev: ApiEvent) => ev.data as Partial<TicketGateData> | null
  store.onEvent('ticket.held', (ev) => {
    const d = gateData(ev)
    const text = holdNotice(d?.ticket?.gate ?? null)
    if (d?.ticket !== undefined && text !== null) setNotice(d.ticket.number, { tone: 'info', text })
  })
  store.onEvent('ticket.refused', (ev) => {
    const d = gateData(ev)
    if (d?.ticket === undefined) return
    const text = `${visibleText(d.plugin ?? '')} refused the move: ${visibleText(d.reason ?? '')}`
    setNotice(d.ticket.number, { tone: 'danger', text })
    say.assertive(`#${d.ticket.number}: ${text}`)
  })
  for (const kind of ['ticket.released', 'ticket.overridden', 'ticket.hold_cancelled']) {
    store.onEvent(kind, (ev) => {
      const n = gateData(ev)?.ticket?.number
      if (n !== undefined) clearNotice(n)
    })
  }

  function clearNotice(n: number): void {
    const old = notices.get(n)
    if (!old) return
    timers.clearTimeout(old.timer)
    notices.delete(n)
  }

  function flash(n: number): void {
    const old = flashes.get(n)
    if (old !== undefined) timers.clearTimeout(old)
    const timer = timers.setTimeout(() => {
      if (flashes.get(n) === timer) flashes.delete(n)
    }, REFUSED_FLASH_MS)
    flashes.set(n, timer)
  }

  function onRefused(n: number, e: unknown): string {
    const text = message(e)
    flash(n)
    setNotice(n, { tone: 'danger', text })
    toast.show({ tone: 'error', title: 'Move refused', message: text })
    say.assertive(`Move of #${n} refused: ${text}`)
    return text
  }

  function onOutcome(n: number, outcome: MoveOutcome): void {
    if (outcome === 'applied') {
      // A move that plugins hold or still have to answer for leaves the card where it was: say why.
      const text = holdNotice(store.view(n)?.gate ?? null)
      if (text !== null) setNotice(n, { tone: 'info', text })
      return
    }
    const text = 'No answer from omo. The board shows what omo did.'
    setNotice(n, { tone: 'info', text })
    toast.show({ tone: 'warn', title: `No answer for #${n}`, message: 'The move may or may not have happened. The board shows what omo did.' })
    say.polite(`No answer for the move of #${n}. The board shows what omo did.`)
  }

  function send(n: number, target: Target, before: number | null, opts?: { reject?: { comment: string } }): Promise<MoveOutcome> {
    clearNotice(n)
    return store.requestMove(n, target.column, before, opts)
  }

  function movedText(t: TicketSummary, target: Target): string {
    if (target.action === 'reorder') return `#${t.number} moved within ${COLUMN_TITLES[target.column]}.`
    if (target.action === 'approve' && target.resultColumn === 'review') {
      return `#${t.number} approved. omo is ${target.resultStage === 'exporting' ? 'exporting' : 'merging'} it.`
    }
    return `#${t.number} moved to ${COLUMN_TITLES[target.resultColumn ?? target.column]}.`
  }

  function drop(n: number, target: Target, before: number | null): void {
    const t = store.view(n)
    if (t === undefined) {
      cancelled(n, 'gone')
      return
    }
    if (!target.allowed) {
      cancelled(n, 'refused', target)
      return
    }
    if (target.action === 'reorder' && before === currentBefore(t)) {
      say.polite(`#${n} stays where it is.`)
      return
    }
    if (target.confirm === 'reject') {
      openDialog({ kind: 'reject', ticket: t, target })
      return
    }
    if (target.confirm === 'stop') {
      const to = target.column === 'ready' ? 'ready' : 'planning'
      openDialog({ kind: 'stop', ticket: t, to, agents: stopAgents(t), target, before })
      return
    }
    say.polite(movedText(t, target))
    send(n, target, before).then(
      (o) => onOutcome(n, o),
      (e: unknown) => {
        onRefused(n, e)
      },
    )
  }

  function stopAgents(t: TicketSummary): StopAgent[] {
    const tickets = t.type === 'story' ? [t, ...store.tasksOf(t.number)] : [t]
    return tickets.flatMap((x) => x.agents.filter((a) => a.state !== 'ended').map((a) => ({ ticket: x.number, role: a.role, name: a.name })))
  }

  function openDialog(d: MoveDialog): void {
    dialogBusy.value = false
    dialogError.value = ''
    dialog.value = d
  }

  function closeDialog(): void {
    if (dialog.value === null) return
    const n = dialog.value.ticket.number
    // Once sent, the move goes on; the card shows how it ends.
    const sent = dialogBusy.value
    dialog.value = null
    dialogBusy.value = false
    dialogError.value = ''
    if (!sent) say.polite(`#${n} was not moved.`)
  }

  function confirmDialog(comment?: string): void {
    const d = dialog.value
    if (d === null || dialogBusy.value) return
    const n = d.ticket.number
    const opts = d.kind === 'reject' ? { reject: { comment: comment ?? '' } } : undefined
    dialogBusy.value = true
    dialogError.value = ''
    say.polite(d.kind === 'reject' ? `#${n} rejected.` : `Stopping #${n}.`)
    const p = send(n, d.target, d.kind === 'stop' ? d.before : null, opts)
    const requestId = store.pending.get(n)?.requestId
    const done = () => {
      if (dialog.value !== d) return
      dialog.value = null
      dialogBusy.value = false
    }
    // The dialog closes once omo has accepted the move; the card shows it as pending until the stream confirms.
    const stopWatch = watch(
      () => {
        // A refusal also ends the overlay; only an answer with an event id says "accepted" here.
        const p = store.pending.get(n)
        return p !== undefined && p.requestId === requestId && p.waitFor !== null
      },
      (accepted) => {
        if (accepted) done()
      },
    )
    p.then(
      (o) => {
        stopWatch()
        done()
        onOutcome(n, o)
      },
      (e: unknown) => {
        stopWatch()
        const text = onRefused(n, e)
        // The toast sits under the open dialog, so the dialog shows the answer itself.
        if (dialog.value === d) {
          dialogBusy.value = false
          dialogError.value = text
        }
      },
    )
  }

  function pickedUp(n: number): void {
    const t = store.view(n)
    if (t === undefined) return
    const all = targets(n)
    const ok = COLUMNS.filter((c) => all[c].allowed).map((c) => `${COLUMN_TITLES[c]} (${actionWords(all[c])})`)
    const where = ok.length === 0 ? 'No column accepts it.' : `It can go to ${ok.join(', ')}.`
    say.polite(`Picked up #${n}, ${t.title}, from ${COLUMN_TITLES[t.column]}. ${where}`)
  }

  function cancelled(n: number, why: CancelReason, target?: Target): void {
    if (why === 'gone') return
    if (why === 'refused' && target !== undefined) {
      say.polite(`#${n} was not moved: ${COLUMN_TITLES[target.column]} does not accept it. ${target.reason ?? ''}`.trim())
      return
    }
    say.polite(`Move of #${n} cancelled.`)
  }

  function follow(moving: Readonly<Ref<{ n: number } | null>>, cancel: () => void): void {
    watch(
      () => {
        const m = moving.value
        if (m === null) return null
        return { n: m.n, column: store.view(m.n)?.column ?? null, own: store.pending.has(m.n) }
      },
      (now, before) => {
        if (now === null || before === null || before === undefined || now.n !== before.n) return
        if (now.column === before.column) return
        // The user's own move of this card started or ended (its overlay came or went): not someone else's change.
        if (now.own || before.own) return
        cancel()
        const text =
          now.column === null
            ? `#${now.n} is no longer on the board. Nothing was moved.`
            : `#${now.n} was moved to ${COLUMN_TITLES[now.column]} meanwhile. Nothing was moved.`
        toast.show({ tone: 'warn', title: `#${now.n} changed while you moved it`, message: text })
        say.assertive(text)
      },
    )
  }

  return {
    targets,
    order,
    unanswered,
    drop,
    dialog: readonly(dialog) as Readonly<Ref<MoveDialog | null>>,
    dialogBusy: readonly(dialogBusy),
    dialogError: readonly(dialogError),
    confirmDialog,
    closeDialog,
    refused: (n) => flashes.has(n),
    notice: (n) => notices.get(n)?.notice,
    pickedUp,
    cancelled,
    follow,
  }
}

const MOVES: InjectionKey<Moves> = Symbol('moves')

/** Makes the board's moves available to the components below the caller. */
export function provideMoves(m: Moves): void {
  provide(MOVES, m)
}

/** The moves provided by the board, or null outside it (the sample board). */
export function useMoves(): Moves | null {
  return inject(MOVES, null)
}

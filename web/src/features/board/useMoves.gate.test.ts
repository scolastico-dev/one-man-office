import { describe, expect, test, vi } from 'vitest'
import type { ApiEvent, TicketGates, TicketGateData, TicketSummary } from '../../api/types'
import type { BoardStore, MoveOutcome } from '../../store'
import { ticket } from './testing'
import { createMoves } from './useMoves'

type Fn = (ev: ApiEvent) => void

const held: TicketGates = { move: 'release', from: 'planning', to: 'ready', gates: [{ plugin: 'pull-base', state: 'held', reason: 'the base is behind', choices: [] }] }

/** A store with one ticket whose move to Ready the server answers as `after` shows it. */
function setup(after: Partial<TicketSummary>) {
  const handlers = new Map<string, Set<Fn>>()
  let t = ticket({ number: 5, column: 'planning' })
  const store = {
    state: { projects: new Map() },
    view: () => t,
    tasksOf: () => [],
    pending: new Map(),
    columns: { value: {} },
    requestMove: vi.fn(async (): Promise<MoveOutcome> => {
      t = { ...t, ...after }
      return 'applied'
    }),
    onEvent(kind: string, fn: Fn) {
      const set = handlers.get(kind) ?? new Set<Fn>()
      handlers.set(kind, set)
      set.add(fn)
      return () => set.delete(fn)
    },
  } as unknown as BoardStore
  const show = vi.fn()
  const toast = { show } as never
  const announcer = { polite: vi.fn(), assertive: vi.fn() } as never
  const moves = createMoves({ store, toast, announcer, timers: { setTimeout: () => 0, clearTimeout: () => undefined } })
  const emit = (kind: string, d: Partial<TicketGateData>) => {
    const data: TicketGateData = { ticket: t, move: 'release', plugin: 'pull-base', reason: '', choices: [], ...d }
    for (const fn of handlers.get(kind) ?? []) fn({ id: 1, kind, ticket: 5, actor: { role: 'system' }, at: '', data })
  }
  return { moves, store, emit, toast: { show } }
}

const toReady = { column: 'ready' as const, allowed: true, action: 'move' as const, resultColumn: 'ready' as const }

describe('moves and plugin gates', () => {
  test('a move that a plugin holds says so on the card instead of failing', async () => {
    const { moves, toast } = setup({ gate: held })
    moves.drop(5, toReady as never, null)
    await vi.waitFor(() => expect(moves.notice(5)).toBeDefined())
    expect(moves.notice(5)).toEqual({ tone: 'info', text: 'held by pull-base: the base is behind' })
    expect(toast.show).not.toHaveBeenCalled()
  })

  test('a move that waits for a plugin to answer says so', async () => {
    const { moves } = setup({ gate: { ...held, gates: [{ ...held.gates[0]!, state: 'pending', reason: '' }] } })
    moves.drop(5, toReady as never, null)
    await vi.waitFor(() => expect(moves.notice(5)).toBeDefined())
    expect(moves.notice(5)?.text).toBe('waiting for pull-base to answer')
  })

  test('a plugin that holds the move later shows its reason', () => {
    const { moves, emit } = setup({})
    emit('ticket.held', { ticket: ticket({ number: 5, gate: held }), plugin: 'pull-base', reason: 'the base is behind' })
    expect(moves.notice(5)).toEqual({ tone: 'info', text: 'held by pull-base: the base is behind' })
  })

  test('a refusal shows its plugin and reason as a refusal', () => {
    const { moves, emit } = setup({})
    emit('ticket.refused', { plugin: 'lint', reason: 'the branch is dirty' })
    expect(moves.notice(5)).toEqual({ tone: 'danger', text: 'lint refused the move: the branch is dirty' })
  })

  test('a go-ahead or a cancel ends the note about the hold', () => {
    const { moves, emit } = setup({})
    emit('ticket.held', { reason: 'x' })
    emit('ticket.released', { plugin: '' })
    expect(moves.notice(5)).toBeUndefined()
  })
})

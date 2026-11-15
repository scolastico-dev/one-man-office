import { describe, expect, test } from 'vitest'
import type { TicketSummary } from '../api/types'
import { ticket } from '../features/board/testing'
import { beforeFor, columnOrder, displayRank, doneOrder } from './ordering'

const task = (number: number, rank: number, over: Partial<TicketSummary> = {}) => ticket({ number, rank, type: 'task', column: 'planning', ...over })
const story = (number: number, rank: number, over: Partial<TicketSummary> = {}) => ticket({ number, rank, type: 'story', column: 'planning', ...over })
const nums = (l: TicketSummary[]) => l.map((t) => t.number)

describe('columnOrder', () => {
  test('stories come first, each group by rank then number; Done newest first', () => {
    const list = [task(1, 5), story(2, 9), task(3, 5), story(4, 1), task(5, 2), task(6, 1, { column: 'ready' })]
    const o = columnOrder(list, 'planning')
    expect(nums(o.stories)).toEqual([4, 2])
    expect(nums(o.tasks)).toEqual([5, 1, 3])
    expect(nums(columnOrder(list, 'ready').tasks)).toEqual([6])
    const done = [
      task(1, 1, { column: 'done', closed_at: '2026-11-01T10:00:00Z' }),
      task(2, 1, { column: 'done', closed_at: '2026-11-03T10:00:00Z' }),
      story(3, 1, { column: 'done', closed_at: '2026-11-02T10:00:00Z' }),
      task(4, 1, { column: 'done', closed_at: '2026-11-03T10:00:00Z' }),
    ]
    expect(nums(doneOrder(done))).toEqual([4, 2, 3, 1])
    const o2 = columnOrder(done, 'done')
    expect(nums(o2.stories)).toEqual([3])
    expect(nums(o2.tasks)).toEqual([4, 2, 1])
  })
})

describe('doneOrder', () => {
  test('compares closing times by value: the server drops trailing zeros of the fraction', () => {
    // Same second. As text, '…:05Z' would sort after both others, because 'Z' > '.'.
    const done = [
      task(1, 1, { column: 'done', closed_at: '2026-11-15T10:00:05Z' }),
      task(2, 1, { column: 'done', closed_at: '2026-11-15T10:00:05.123456789Z' }),
      task(3, 1, { column: 'done', closed_at: '2026-11-15T10:00:05.5Z' }),
      task(4, 1, { column: 'done', closed_at: null }),
    ]
    expect(nums(doneOrder(done))).toEqual([3, 2, 1, 4])
  })
})

describe('beforeFor', () => {
  // Full rank order: T1(1) S2(2) T3(3) S4(4) T5(5) T6(6)
  const board = [task(1, 1), story(2, 2), task(3, 3), story(4, 4), task(5, 5), task(6, 6)]
  const dragged = (n: number) => board.find((t) => t.number === n)!

  test('reorders within the group: next card of the group, else the card after the group\'s last card in rank order, else null', () => {
    // Tasks without #3: 1, 5, 6
    expect(beforeFor(board, dragged(3), 0)).toBe(1)
    expect(beforeFor(board, dragged(3), 1)).toBe(5)
    expect(beforeFor(board, dragged(3), 2)).toBe(6)
    // Last in the group: the card after task 6 in full order is none.
    expect(beforeFor(board, dragged(3), 3)).toBeNull()
    // Stories without #2: only 4 → at the end the card after story 4 is task 5.
    expect(beforeFor(board, dragged(2), 0)).toBe(4)
    expect(beforeFor(board, dragged(2), 1)).toBe(5)
    // Without #4 the last story is 2, followed by task 3.
    expect(beforeFor(board, dragged(4), 1)).toBe(3)
  })

  test('an out of range position is clamped', () => {
    expect(beforeFor(board, dragged(3), -4)).toBe(1)
    expect(beforeFor(board, dragged(3), 99)).toBeNull()
  })

  test('a card alone in its group keeps its place', () => {
    const l = [task(1, 1), story(2, 2), task(3, 3)]
    expect(beforeFor(l, l[1]!, 0)).toBe(3)
    const l2 = [story(2, 1), task(3, 2)]
    expect(beforeFor(l2, l2[1]!, 0)).toBeNull()
    expect(beforeFor([task(1, 1), story(2, 2)], task(1, 1), 0)).toBe(2)
    expect(beforeFor([task(1, 1)], task(1, 1), 0)).toBeNull()
  })

  test('ties in rank are broken by number', () => {
    const l = [task(2, 5), task(1, 5), task(3, 5)]
    expect(beforeFor(l, l[2]!, 0)).toBe(1)
    expect(beforeFor(l, l[2]!, 2)).toBeNull()
  })

  test('applying before to the server ranking gives the order the user saw (property)', () => {
    let seed = 12345
    const rnd = (n: number) => {
      seed = (seed * 1664525 + 1013904223) % 4294967296
      return Math.floor((seed / 4294967296) * n)
    }
    for (let round = 0; round < 400; round++) {
      const size = 1 + rnd(9)
      const cards: TicketSummary[] = []
      for (let i = 1; i <= size; i++) cards.push((rnd(3) === 0 ? story : task)(i, rnd(4)))
      const d = cards[rnd(size)]!
      const group = columnOrder(cards, 'planning')[d.type === 'story' ? 'stories' : 'tasks'].filter((t) => t.number !== d.number)
      const at = rnd(group.length + 1)
      const wanted = [...nums(group)]
      wanted.splice(at, 0, d.number)

      const before = beforeFor(cards, d, at)
      // The server's ranking: everything in rank order, the card inserted directly before `before`.
      const full = cards.filter((t) => t.number !== d.number).sort((a, b) => a.rank - b.rank || a.number - b.number)
      const i = before === null ? full.length : full.findIndex((t) => t.number === before)
      expect(i).toBeGreaterThanOrEqual(0)
      full.splice(i, 0, d)
      // Ranks as the server renumbers them, then the board's display order.
      const after = full.map((t, k) => ({ ...t, rank: k }))
      const o = columnOrder(after, 'planning')
      expect(nums(d.type === 'story' ? o.stories : o.tasks)).toEqual(wanted)
      // The other group keeps its order.
      const other = d.type === 'story' ? 'tasks' : 'stories'
      expect(nums(o[other])).toEqual(nums(columnOrder(cards, 'planning')[other]))
    }
  })

  test('dropped last in its group, the card lands directly after its predecessor in rank order', () => {
    const l = [task(1, 1), story(2, 2), task(3, 3), story(4, 4), task(5, 5)]
    // Move task 1 to the end of the tasks: stays before nothing.
    expect(beforeFor(l, l[0]!, 2)).toBeNull()
    // Move story 2 behind story 4: lands before task 5, right after story 4.
    expect(beforeFor(l, l[1]!, 1)).toBe(5)
  })
})

describe('displayRank', () => {
  const cards = [task(1, 10), task(2, 20), task(3, 30)]
  test('sits between its neighbours, above the first or below the last', () => {
    expect(displayRank(cards, 2)).toBe(15)
    expect(displayRank(cards, 1)).toBe(10 - 1024)
    expect(displayRank(cards, null)).toBe(30 + 1024)
    expect(displayRank([], null)).toBe(1024)
  })
  test('leaves out the dragged card', () => {
    expect(displayRank(cards, 3, 2)).toBe(20)
    expect(displayRank(cards, null, 3)).toBe(20 + 1024)
  })
  test('an unknown before goes to the end', () => {
    expect(displayRank(cards, 99)).toBe(30 + 1024)
  })
})

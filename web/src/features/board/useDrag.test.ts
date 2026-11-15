import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { announcements } from '../../components/ui/useAnnouncer'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { project } from '../../testing/fakeOmo'
import { harness, mountWith, ok, refuse, type Harness, type Sent } from '../create/dialogTesting'
import LiveBoard from './LiveBoard.vue'
import { ticket } from './testing'
import { LONG_PRESS_MS } from './useDrag'

// Layout of the fake page: each column is 300 px wide, side by side; the cards
// of a column are 40 px high, 50 px apart, from y = 100.
const COL_W = 300
const CARD_TOP = 100
const CARD_STEP = 50

const on = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '' })
const agent = (name: string, role: 'developer' | 'pm' = 'developer') => ({ session: `s-${name}`, role, name, state: 'running' as const })

let mounted: VueWrapper | null = null
let h: Harness

function rectOf(el: Element): DOMRect {
  const li = el as HTMLElement
  const section = li.closest('section[data-drop-column]')
  if (li.tagName === 'LI' && li.dataset.ticket && section) {
    const col = COLUMNS.indexOf(section.getAttribute('data-drop-column') as Column)
    const i = [...section.querySelectorAll('li[data-ticket]')].indexOf(li)
    return new DOMRect(col * COL_W + 10, CARD_TOP + i * CARD_STEP, COL_W - 20, 40)
  }
  return new DOMRect(0, 0, 0, 0)
}

/** The point where the pointer is over column `c`, at height `y`. */
const at = (c: Column, y = 20) => ({ x: COLUMNS.indexOf(c) * COL_W + 150, y })
/** A point just above card `i` (0 = first card) of a column. */
const aboveCard = (c: Column, i: number) => at(c, CARD_TOP + i * CARD_STEP + 5)
/** A point below every card of a column. */
const belowAll = (c: Column) => at(c, 2000)
const OUTSIDE = { x: 5000, y: 20 }

beforeEach(() => {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    return rectOf(this)
  })
  document.elementFromPoint = (x: number) => {
    const c = COLUMNS[Math.floor(x / COL_W)]
    return c === undefined ? document.body : document.querySelector(`section[data-drop-column="${c}"]`)
  }
})

afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.restoreAllMocks()
  vi.useRealTimers()
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

async function board(tickets: TicketSummary[]): Promise<VueWrapper> {
  h = await harness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('api')]
  })
  h.omo.stream!.onState('live')
  mounted = await mountWith(h, LiveBoard, {})
  await flushPromises()
  return mounted
}

function pointer(type: string, init: { x: number; y: number; pointerType?: string }, target: EventTarget = window): void {
  const e = new PointerEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: init.x,
    clientY: init.y,
    pointerId: 1,
    pointerType: init.pointerType ?? 'mouse',
    isPrimary: true,
    button: 0,
  })
  target.dispatchEvent(e)
}

const cardEl = (n: number) => document.querySelector<HTMLElement>(`li[data-ticket="${n}"]`)!

/** Presses on card `n` with the mouse at its own place. */
function press(n: number, pointerType = 'mouse'): { x: number; y: number } {
  const r = rectOf(cardEl(n))
  const p = { x: r.left + 20, y: r.top + 20 }
  pointer('pointerdown', { ...p, pointerType }, cardEl(n).querySelector('a')!)
  return p
}

async function moveTo(p: { x: number; y: number }, pointerType = 'mouse') {
  pointer('pointermove', { ...p, pointerType })
  await nextTick()
}

async function release(p: { x: number; y: number }) {
  pointer('pointerup', p)
  await flushPromises()
}

/** Picks card `n` up and moves it over `p`. */
async function dragOver(n: number, p: { x: number; y: number }) {
  const start = press(n)
  await moveTo({ x: start.x + 10, y: start.y })
  await moveTo(p)
}

async function dragTo(n: number, p: { x: number; y: number }) {
  await dragOver(n, p)
  await release(p)
}

const section = (c: Column) => document.querySelector<HTMLElement>(`section[data-drop-column="${c}"]`)!
const moves = (): Sent[] => h.sent.filter((s) => s.method === 'POST')

describe('picking up', () => {
  test('mouse drag starts after 4 px, touch after a 300 ms long press', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    const start = press(1)
    await moveTo({ x: start.x + 3, y: start.y })
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)
    await moveTo({ x: start.x + 4, y: start.y })
    expect(cardEl(1).classList.contains('card--dragging')).toBe(true)
    expect(document.querySelector('[data-drag-ghost]')).not.toBeNull()
    await nextTick()
    expect(announcements.polite.value).toContain('Picked up #1')
    expect(announcements.polite.value).toContain('Ready For Dev (approves)')
    await release(start)
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)
    expect(document.querySelector('[data-drag-ghost]')).toBeNull()

    vi.useFakeTimers()
    const t = press(1, 'touch')
    await moveTo({ x: t.x + 2, y: t.y }, 'touch')
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)
    vi.advanceTimersByTime(LONG_PRESS_MS - 1)
    await nextTick()
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)
    vi.advanceTimersByTime(1)
    await nextTick()
    expect(cardEl(1).classList.contains('card--dragging')).toBe(true)
    pointer('pointercancel', t)
    await nextTick()
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)

    // A finger that moves before the long press scrolls instead.
    const s = press(1, 'touch')
    await moveTo({ x: s.x, y: s.y + 20 }, 'touch')
    vi.advanceTimersByTime(LONG_PRESS_MS)
    await nextTick()
    expect(cardEl(1).classList.contains('card--dragging')).toBe(false)
  })

  test('the story chip and other controls on a card are not drag handles', async () => {
    await board([ticket({ number: 1, type: 'story', title: 'S' }), ticket({ number: 2, story: 1, projects: [on('api')] })])
    const chip = cardEl(2).querySelector('[data-chip="story"]')!
    const r = rectOf(cardEl(2))
    pointer('pointerdown', { x: r.left + 5, y: r.top + 5 }, chip)
    await moveTo({ x: r.left + 60, y: r.top + 5 })
    expect(cardEl(2).classList.contains('card--dragging')).toBe(false)
    await release({ x: r.left + 60, y: r.top + 5 })
  })
})

describe('targets', () => {
  test('while dragging, refused columns are aria-disabled with their reason; a Done card greys all five', async () => {
    await board([
      ticket({ number: 1, projects: [on('api')] }),
      ticket({ number: 2, column: 'done', resolution: 'completed', closed_at: '2026-11-15T10:00:00Z' }),
    ])
    await dragOver(1, at('planning'))
    expect(section('ready').getAttribute('aria-disabled')).toBeNull()
    expect(section('ready').querySelector('[data-drop-action]')!.textContent).toBe('Drop to approve.')
    expect(section('planning').classList.contains('column--over')).toBe(true)
    for (const c of ['in_progress', 'review', 'done'] as const) {
      expect(section(c).getAttribute('aria-disabled')).toBe('true')
      expect(section(c).querySelector('[data-reason]')!.textContent).not.toBe('')
    }
    expect(section('in_progress').querySelector('[data-reason]')!.textContent).toBe('A ticket enters In Progress when its agent starts or its review is rejected.')
    pointer('keydown', { x: 0, y: 0 })
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await nextTick()
    expect(section('in_progress').getAttribute('aria-disabled')).toBeNull()
    expect(document.querySelector('[data-reason]')).toBeNull()

    await dragOver(2, at('done'))
    for (const c of COLUMNS) {
      expect(section(c).getAttribute('aria-disabled')).toBe('true')
      expect(section(c).querySelector('[data-reason]')!.textContent).toBe('Done tickets stay where they are.')
    }
    await release(at('done'))
    expect(moves()).toEqual([])
  })

  test('a busy card greys Planning and Ready For Dev as stop targets with the busy reason', async () => {
    await board([ticket({ number: 4, column: 'in_progress', busy: true, agents: [agent('dev')] })])
    await dragOver(4, at('planning'))
    const reason = 'omo is starting work on or merging #4; try again in a moment.'
    for (const c of ['planning', 'ready'] as const) {
      expect(section(c).getAttribute('aria-disabled')).toBe('true')
      expect(section(c).querySelector('[data-reason]')!.textContent).toBe(reason)
    }
    expect(section('in_progress').getAttribute('aria-disabled')).toBeNull()
    await release(at('planning'))
    expect(moves()).toEqual([])
  })
})

describe('dropping', () => {
  test('dropping on a refused column or outside sends nothing', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    await dragTo(1, at('review'))
    expect(announcements.polite.value).toContain('Review does not accept it')
    await dragTo(1, OUTSIDE)
    await dragOver(1, at('ready'))
    window.dispatchEvent(new Event('blur'))
    await release(at('ready'))
    await dragOver(1, at('ready'))
    pointer('pointercancel', at('ready'))
    await release(at('ready'))
    expect(moves()).toEqual([])
    expect(cardEl(1).closest('section')!.dataset.dropColumn).toBe('planning')
  })

  test('within a column the drop sends before = the card below the drop position', async () => {
    await board([1, 2, 3, 4].map((n) => ticket({ number: n, rank: n * 10 })))
    h.respond.write = () => ok({})
    // #4 dropped above the second card (#2).
    await dragOver(4, aboveCard('planning', 1))
    expect(cardEl(2).classList.contains('card--drop-before')).toBe(true)
    await release(aboveCard('planning', 1))
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/4/move', body: { column: 'planning', before: 2 } }])
    // #1 dropped below all: the end.
    await dragTo(1, belowAll('planning'))
    expect(moves()[1]!.body).toEqual({ column: 'planning', before: null })
  })

  test('dropping a card where it already is sends nothing', async () => {
    await board([1, 2, 3].map((n) => ticket({ number: n, rank: n * 10 })))
    await dragTo(2, aboveCard('planning', 2))
    await dragTo(3, belowAll('planning'))
    expect(moves()).toEqual([])
    expect(announcements.polite.value).toBe('#3 stays where it is.')
  })

  test('a story can only be dropped among stories and a task only among tasks', async () => {
    await board([
      ticket({ number: 1, type: 'story', rank: 10 }),
      ticket({ number: 2, type: 'story', rank: 20 }),
      ticket({ number: 3, rank: 5 }),
      ticket({ number: 4, rank: 30 }),
    ])
    h.respond.write = () => ok({})
    // Shown order: stories #1 #2, then tasks #3 #4. A story dropped among the tasks lands last among the stories.
    await dragOver(1, aboveCard('planning', 3))
    expect(cardEl(2).classList.contains('card--drop-after')).toBe(true)
    expect(cardEl(4).classList.contains('card--drop-before')).toBe(false)
    await release(aboveCard('planning', 3))
    // After #2 in rank order comes #4.
    expect(moves()[0]!.body).toEqual({ column: 'planning', before: 4 })
    // A task dropped on top of the stories lands first among the tasks.
    await dragOver(4, aboveCard('planning', 0))
    expect(cardEl(3).classList.contains('card--drop-before')).toBe(true)
    await release(aboveCard('planning', 0))
    expect(moves()[1]!.body).toEqual({ column: 'planning', before: 3 })
  })

  test('an approval shows the card in its new column at once, as pending', async () => {
    await board([ticket({ number: 1, projects: [on('api')] }), ticket({ number: 2, column: 'ready', rank: 5 })])
    let answer!: () => void
    h.respond.write = () => new Promise((resolve) => (answer = () => resolve({ data: {}, eventId: 99 })))
    await dragTo(1, aboveCard('ready', 0))
    expect(moves()[0]).toEqual({ method: 'POST', path: '/api/tickets/1/move', body: { column: 'ready', before: 2 } })
    expect(cardEl(1).closest('section')!.dataset.dropColumn).toBe('ready')
    expect(cardEl(1).classList.contains('card--pending')).toBe(true)
    expect(announcements.polite.value).toBe('#1 moved to Ready For Dev.')
    answer()
    await flushPromises()
    expect(cardEl(1).classList.contains('card--pending')).toBe(true)
    h.omo.change('ticket.moved', ticket({ number: 1, column: 'ready', rank: 1, projects: [on('api')] }))
    h.omo.eventId = 98
    h.omo.emit('ticket.updated', { ticket: ticket({ number: 2, column: 'ready', rank: 5 }) }, 2)
    await flushPromises()
    expect(cardEl(1).classList.contains('card--pending')).toBe(false)
  })

  test('a refusal puts the card back, flashes it, shows the server message on it and announces it', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    h.respond.write = () => refuse('validation', 'project api is missing')
    await dragTo(1, at('ready'))
    expect(cardEl(1).closest('section')!.dataset.dropColumn).toBe('planning')
    expect(cardEl(1).classList.contains('card--refused')).toBe(true)
    expect(cardEl(1).querySelector('[data-move-notice]')!.textContent).toBe('project api is missing')
    expect(activeToasts.map((t) => [t.title, t.message])).toContainEqual(['Move refused', 'project api is missing'])
    await nextTick()
    expect(announcements.assertive.value).toBe('Move of #1 refused: project api is missing')
  })

  test('a lost answer claims neither success nor failure', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    await board([ticket({ number: 1, projects: [on('api')] })])
    h.respond.write = () => refuse('network', 'Cannot reach omo.', undefined, 0)
    await dragTo(1, at('ready'))
    vi.advanceTimersByTime(10_000)
    await flushPromises()
    expect(cardEl(1).querySelector('[data-move-notice]')!.textContent).toBe('No answer from omo. The board shows what omo did.')
    expect(activeToasts.map((t) => t.title)).toContain('No answer for #1')
    expect(activeToasts.map((t) => t.title)).not.toContain('Move refused')
    expect(cardEl(1).classList.contains('card--refused')).toBe(false)
  })
})

describe('dialogs', () => {
  test('rejecting asks for a comment; nothing is sent before it is confirmed and cancel leaves the card', async () => {
    await board([ticket({ number: 5, column: 'review', review_stage: 'human', agents: [] })])
    h.respond.write = () => ok({})
    await dragTo(5, at('in_progress'))
    const dialog = () => document.querySelector('dialog[open]')
    expect(dialog()!.textContent).toContain('Reject #5?')
    expect(moves()).toEqual([])
    expect(cardEl(5).closest('section')!.dataset.dropColumn).toBe('review')
    ;(dialog()!.querySelector('.dialog__actions button') as HTMLButtonElement).click()
    await flushPromises()
    expect(dialog()).toBeNull()
    expect(moves()).toEqual([])

    await dragTo(5, at('in_progress'))
    // An empty comment is not sent.
    dialog()!.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(dialog()!.textContent).toContain('Write what needs to change.')
    expect(moves()).toEqual([])
    const area = dialog()!.querySelector('textarea')!
    area.value = 'Tests are missing.'
    area.dispatchEvent(new Event('input'))
    dialog()!.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/5/reject', body: { comment: 'Tests are missing.' } }])
    expect(dialog()).toBeNull()
  })

  test('a refused rejection keeps the dialog open with the server message', async () => {
    await board([ticket({ number: 5, column: 'review', review_stage: 'human' })])
    h.respond.write = () => refuse('conflict', 'The review is no longer waiting for you.', undefined, 409)
    await dragTo(5, at('in_progress'))
    const area = document.querySelector('dialog[open] textarea') as HTMLTextAreaElement
    area.value = 'No.'
    area.dispatchEvent(new Event('input'))
    document.querySelector('dialog[open] form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(document.querySelector('dialog[open] [data-move-error]')!.textContent).toBe('The review is no longer waiting for you.')
    expect((document.querySelector('dialog[open] textarea') as HTMLTextAreaElement).value).toBe('No.')
    expect(cardEl(5).closest('section')!.dataset.dropColumn).toBe('review')
  })

  test('stopping asks first, names the agents of a story and its tasks, and cancel sends nothing', async () => {
    await board([
      ticket({ number: 1, type: 'story', column: 'in_progress', agents: [agent('Ada', 'pm')], projects: [on('api')] }),
      ticket({ number: 2, story: 1, column: 'in_progress', agents: [agent('Bob')] }),
      ticket({ number: 3, story: 1, column: 'review', review_stage: 'agent', agents: [] }),
    ])
    h.respond.write = () => ok({})
    await dragTo(1, at('ready'))
    const dialog = () => document.querySelector('dialog[open]')
    expect(dialog()!.textContent).toContain('Stop #1 and move it to Ready For Dev?')
    expect(dialog()!.textContent).toContain('The agent stops. The branch and worktree are kept.')
    const agents = [...dialog()!.querySelectorAll('[data-stop-agents] li')].map((li) => li.textContent!.trim().replace(/\s+/g, ' '))
    expect(agents).toEqual(['#1 product manager · Ada', '#2 developer · Bob'])
    expect(dialog()!.textContent).toContain('Its tasks in In Progress or Review return to Ready For Dev.')
    expect(moves()).toEqual([])
    ;(dialog()!.querySelector('.dialog__actions button') as HTMLButtonElement).click()
    await flushPromises()
    expect(moves()).toEqual([])

    await dragTo(1, at('planning'))
    expect(dialog()!.textContent).toContain('Stop #1 and move it to Planning?')
    ;(dialog()!.querySelector('[data-primary]') as HTMLButtonElement).click()
    await flushPromises()
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/1/move', body: { column: 'planning', before: null } }])
    expect(dialog()).toBeNull()
  })

  test('stopping a ticket no agent works on says so and does not claim an agent stops', async () => {
    await board([ticket({ number: 4, column: 'in_progress', agents: [] })])
    h.respond.write = () => ok({})
    await dragTo(4, at('planning'))
    const dialog = () => document.querySelector('dialog[open]')
    expect(dialog()!.textContent).toContain('Stop #4 and move it to Planning?')
    expect(dialog()!.textContent).not.toContain('The agent stops.')
    expect(dialog()!.textContent).toContain('The branch and worktree are kept.')
    expect(dialog()!.querySelector('[data-stop-no-agents]')!.textContent).toBe('No agent is working on it.')
    expect(dialog()!.querySelector('[data-stop-agents]')).toBeNull()
    ;(dialog()!.querySelector('[data-primary]') as HTMLButtonElement).click()
    await flushPromises()
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/4/move', body: { column: 'planning', before: null } }])
  })
})

describe('changes under the drag', () => {
  test('the dragged card moved by someone else ends the drag with a notice', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    await dragOver(1, at('ready'))
    h.omo.change('ticket.moved', ticket({ number: 1, column: 'ready', projects: [on('api')] }))
    await flushPromises()
    expect(document.querySelector('[data-drag-ghost]')).toBeNull()
    expect(activeToasts.map((t) => t.message)).toContain('#1 was moved to Ready For Dev meanwhile. Nothing was moved.')
    await release(at('planning'))
    expect(moves()).toEqual([])
  })

  test('a card arriving under a still pointer moves the drop place with it', async () => {
    await board([1, 2, 3].map((n) => ticket({ number: n, rank: n * 10 })))
    h.respond.write = () => ok({})
    await dragOver(3, aboveCard('planning', 1))
    expect(cardEl(2).classList.contains('card--drop-before')).toBe(true)
    // #9 arrives at the top: the second shown card is now #1.
    h.omo.change('ticket.created', ticket({ number: 9, rank: 1 }))
    await flushPromises()
    await nextTick()
    expect(cardEl(1).classList.contains('card--drop-before')).toBe(true)
    await release(aboveCard('planning', 1))
    expect(moves()[0]!.body).toEqual({ column: 'planning', before: 1 })
  })
})

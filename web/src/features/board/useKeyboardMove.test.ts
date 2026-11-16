import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { ApiError } from '../../api/client'
import { afterEach, describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { announcements } from '../../components/ui/useAnnouncer'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { project } from '../../testing/fakeOmo'
import { harness, mountWith, ok, refuse, type Harness, type Sent } from '../create/dialogTesting'
import LiveBoard from './LiveBoard.vue'
import { ticket } from './testing'

const on = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' })

let mounted: VueWrapper | null = null
let h: Harness

afterEach(() => {
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

async function board(tickets: TicketSummary[]): Promise<void> {
  h = await harness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('api')]
  })
  h.omo.stream!.onState('live')
  mounted = await mountWith(h, LiveBoard, {})
  await flushPromises()
}

const link = (n: number) => document.querySelector<HTMLElement>(`li[data-ticket="${n}"] [data-card-link]`)!
const section = (c: Column) => document.querySelector<HTMLElement>(`section[data-drop-column="${c}"]`)!
const columnOf = (n: number) => document.querySelector<HTMLElement>(`li[data-ticket="${n}"]`)!.closest('section')!.dataset.dropColumn
const moves = (): Sent[] => h.sent.filter((s) => s.method === 'POST')
const polite = () => announcements.polite.value

/** Presses a key on whatever has the focus, and lets the page and the announcer catch up. */
async function press(key: string): Promise<void> {
  const el = (document.activeElement ?? document.body) as HTMLElement
  el.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))
  await flushPromises()
  await nextTick()
}

async function focus(n: number): Promise<void> {
  link(n).focus()
  await nextTick()
}

describe('a move after a move that has no answer yet', () => {
  test('never names the card whose own move is still pending as the one to go before', async () => {
    await board([
      ticket({ number: 1, rank: 1000, projects: [on('api')] }),
      ticket({ number: 2, rank: 2000, projects: [on('api')] }),
      ticket({ number: 3, rank: 3000, projects: [on('api')] }),
    ])
    let release: () => void = () => {}
    let calls = 0
    h.respond.write = () => {
      calls++
      if (calls > 1) return ok({})
      return new Promise((res) => (release = () => res(ok({}))))
    }
    // #2 goes to the top, above #1; omo has not answered yet.
    await focus(2)
    await press(' ')
    await press('ArrowUp')
    await press(' ')
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/2/move', body: { column: 'planning', before: 1 } }])
    // #3 goes to the top: #2 is shown there, but omo has not placed it, so #3 is put before #1.
    await focus(3)
    await press(' ')
    await press('ArrowUp')
    await press('ArrowUp')
    await press(' ')
    release()
    await flushPromises()
    expect(moves().map((m) => m.path)).toEqual(['/api/tickets/2/move', '/api/tickets/3/move'])
    expect(moves()[1]?.body).toEqual({ column: 'planning', before: 1 })
  })
})

describe('picking a card up again while its own move is pending', () => {
  test('the end of that move (a refusal) is not reported as someone else moving the card', async () => {
    await board([ticket({ number: 1, title: 'Login page', projects: [on('api')] })])
    let refuseIt: () => void = () => {}
    h.respond.write = () => new Promise((_, rej) => (refuseIt = () => rej(new ApiError(409, 'invalid_transition', 'no'))))
    await focus(1)
    await press(' ')
    await press('ArrowRight')
    await press(' ')
    expect(columnOf(1)).toBe('ready')
    // Picked up again while omo has not answered: the card is shown in Ready For Dev.
    await focus(1)
    await press(' ')
    expect(document.querySelector('li[data-ticket="1"]')!.classList.contains('card--dragging')).toBe(true)
    refuseIt()
    await flushPromises()
    await nextTick()
    // The overlay ended by itself: the card is back in Planning, and what the user holds is not cancelled with a false claim.
    expect(columnOf(1)).toBe('planning')
    expect(activeToasts.map((t) => t.message).filter((m) => m?.includes('meanwhile'))).toEqual([])
    expect(announcements.assertive.value).not.toContain('meanwhile')
    expect(document.querySelector('li[data-ticket="1"]')!.classList.contains('card--dragging')).toBe(true)
  })
})

describe('keyboard moving', () => {
  test('Space picks up, arrows skip refused columns, Space drops through the same handler as dragging', async () => {
    await board([ticket({ number: 1, title: 'Login page', projects: [on('api')] }), ticket({ number: 2, column: 'ready', rank: 5 })])
    // No answer yet: the card shows at its target until omo answers.
    h.respond.write = () => new Promise(() => {})
    await focus(1)
    await press(' ')
    expect(polite()).toBe(
      'Picked up #1, Login page. In Planning, position 1 of 1. It can go to Planning (changes priority), Ready For Dev (approves). Arrow keys move, Space drops, Escape cancels.',
    )
    expect(document.querySelector('li[data-ticket="1"]')!.classList.contains('card--dragging')).toBe(true)
    // The columns show the same greyed state as for a drag.
    for (const c of ['in_progress', 'review', 'done'] as const) expect(section(c).getAttribute('aria-disabled')).toBe('true')

    await press('ArrowRight')
    expect(polite()).toBe('Ready For Dev: approves. Position 2 of 2.')
    expect(section('ready').classList.contains('column--over')).toBe(true)

    // Nothing further right accepts it: the refused columns are named with their reasons.
    await press('ArrowRight')
    expect(polite()).toContain('No column to the right accepts #1.')
    expect(polite()).toContain('In Progress: A ticket enters In Progress when its agent starts or its review is rejected.')
    expect(polite()).toContain('Review: A ticket enters Review when its agent hands it in.')
    expect(section('ready').classList.contains('column--over')).toBe(true)

    await press(' ')
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/1/move', body: { column: 'ready', before: null } }])
    expect(polite()).toBe('#1 moved to Ready For Dev.')
    expect(columnOf(1)).toBe('ready')
    // The card was drawn again in its new column and still has the focus.
    expect(document.activeElement).toBe(link(1))
  })

  test('up and down change the place within the group; Enter drops', async () => {
    await board([1, 2, 3].map((n) => ticket({ number: n, rank: n * 10 })))
    h.respond.write = () => ok({})
    await focus(3)
    await press('m')
    expect(polite()).toContain('In Planning, position 3 of 3.')
    await press('ArrowDown')
    expect(polite()).toBe('#3 is already at the bottom.')
    await press('ArrowUp')
    expect(polite()).toBe('Position 2 of 3.')
    expect(document.querySelector('li[data-ticket="2"]')!.classList.contains('card--drop-before')).toBe(true)
    await press('ArrowUp')
    expect(polite()).toBe('Position 1 of 3.')
    await press('Enter')
    expect(moves()).toEqual([{ method: 'POST', path: '/api/tickets/3/move', body: { column: 'planning', before: 1 } }])
    expect(polite()).toBe('#3 moved within Planning.')
    expect(document.activeElement).toBe(link(3))
  })

  test('Escape cancels and focus stays on the card', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    await focus(1)
    await press(' ')
    await press('ArrowRight')
    await press('Escape')
    expect(polite()).toBe('Move of #1 cancelled.')
    expect(document.activeElement).toBe(link(1))
    expect(document.querySelector('.card--dragging')).toBeNull()
    expect(section('ready').classList.contains('column--over')).toBe(false)
    expect(moves()).toEqual([])
    // Tabbing away ends a move as well.
    await press(' ')
    const away = document.createElement('button')
    document.body.append(away)
    link(1).dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: away }))
    await nextTick()
    expect(document.querySelector('.card--dragging')).toBeNull()
    away.remove()
  })

  test('a move that rejects asks for the comment, as a drag does', async () => {
    await board([ticket({ number: 5, column: 'review', review_stage: 'human' })])
    h.respond.write = () => ok({})
    await focus(5)
    await press(' ')
    await press('ArrowLeft')
    expect(polite()).toBe('In Progress: rejects.')
    await press(' ')
    const dialog = document.querySelector('dialog[open]')!
    expect(dialog.textContent).toContain('Reject #5?')
    expect(moves()).toEqual([])
  })

  test('a Done card can be picked up, but every column says why it does not accept it', async () => {
    await board([ticket({ number: 2, column: 'done', resolution: 'completed', closed_at: '2026-11-15T10:00:00Z' })])
    await focus(2)
    await press(' ')
    expect(polite()).toContain('No column accepts it. Done: Done tickets stay where they are.')
    for (const c of COLUMNS) expect(section(c).getAttribute('aria-disabled')).toBe('true')
    await press('ArrowLeft')
    expect(polite()).toBe('No other column accepts #2. Review, In Progress, Ready For Dev, Planning: Done tickets stay where they are.')
    await press('ArrowUp')
    expect(polite()).toBe('Done: Done tickets stay where they are.')
    await press(' ')
    expect(polite()).toBe('#2 was not moved: Done does not accept it. Done tickets stay where they are.')
    expect(moves()).toEqual([])
  })

  test('a refused move puts the card back and keeps the focus on it', async () => {
    await board([ticket({ number: 1, projects: [on('api')] })])
    h.respond.write = () => refuse('validation', 'project api is missing')
    await focus(1)
    await press(' ')
    await press('ArrowRight')
    await press(' ')
    expect(columnOf(1)).toBe('planning')
    expect(document.activeElement).toBe(link(1))
    expect(announcements.assertive.value).toBe('Move of #1 refused: project api is missing')
  })

  test('the focus stays on the card when the stream draws the board anew', async () => {
    await board([ticket({ number: 1 }), ticket({ number: 2, rank: 5 })])
    await focus(1)
    // A story arrives: the column now shows groups, and every card in it is drawn anew.
    h.omo.change('ticket.created', ticket({ number: 9, type: 'story' }))
    await flushPromises()
    expect(section('planning').textContent).toContain('Stories')
    expect(document.activeElement).toBe(link(1))
    const said = polite()
    // Moves made by others are not announced.
    h.omo.change('ticket.moved', ticket({ number: 2, column: 'ready' }))
    await flushPromises()
    expect(polite()).toBe(said)
    expect(document.activeElement).toBe(link(1))
  })

  test('a card that a filter hides hands the focus to the card now at its place', async () => {
    await board([ticket({ number: 1, rank: 1, title: 'First' }), ticket({ number: 2, rank: 2, title: 'Second' }), ticket({ number: 3, rank: 3, title: 'Third' })])
    await focus(2)
    await mounted!.vm.$router.push({ path: '/', query: { q: 'i' } })
    await flushPromises()
    expect(document.querySelector('li[data-ticket="2"]')).toBeNull()
    expect(document.activeElement).toBe(link(3))
  })
})

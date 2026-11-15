import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { dragTargets, type RuleContext, type Target } from '../../board/moveRules'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { project } from '../../testing/fakeOmo'
import { harness, mountWith, type Harness } from '../create/dialogTesting'
import LiveBoard from './LiveBoard.vue'
import MoveMenu from './MoveMenu.vue'
import { ticket } from './testing'

const on = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '', export_url: '' })
const ctx: RuleContext = { project: (id) => ({ ...project(id) }), storyHasLiveAgent: () => false }

let mounted: VueWrapper | null = null

afterEach(() => {
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

/** The menu of `t` on a board holding `all`, with the rules the board uses. */
function menu(t: TicketSummary, all: TicketSummary[] = [t]): VueWrapper {
  const byNumber = new Map(all.map((x) => [x.number, x]))
  mounted = mount(MoveMenu, {
    props: {
      ticket: t,
      moves: {
        targets: (n: number) => dragTargets(byNumber.get(n)!, ctx),
        order: (c: Column) => all.filter((x) => x.column === c).sort((a, b) => (a.type === b.type ? a.rank - b.rank : a.type === 'story' ? -1 : 1)),
      },
    },
    attachTo: document.body,
  })
  return mounted
}

async function open(w: VueWrapper): Promise<{ label: string; disabled: boolean; hint: string }[]> {
  await w.get('[data-move-menu]').trigger('focusin')
  await nextTick()
  await w.get('button').trigger('keydown', { key: 'ArrowDown' })
  await nextTick()
  return w.findAll('[role="menuitem"]').map((li) => ({
    label: li.get('.menu__label').text(),
    disabled: li.attributes('aria-disabled') === 'true',
    hint: li.find('.menu__hint').exists() ? li.get('.menu__hint').text() : '',
  }))
}

describe('the move menu', () => {
  test('the menu lists all five columns with refused ones disabled and their reasons', async () => {
    const w = menu(ticket({ number: 12, projects: [on('api')], rank: 2 }), [ticket({ number: 12, projects: [on('api')], rank: 2 }), ticket({ number: 3, rank: 1 })])
    expect(w.get('button').attributes('aria-label')).toBe('Move #12…')
    expect(await open(w)).toEqual([
      { label: 'Planning', disabled: true, hint: '#12 is in this column.' },
      { label: 'Ready For Dev', disabled: false, hint: 'Approves.' },
      { label: 'In Progress', disabled: true, hint: 'A ticket enters In Progress when its agent starts or its review is rejected.' },
      { label: 'Review', disabled: true, hint: 'A ticket enters Review when its agent hands it in.' },
      { label: 'Done', disabled: true, hint: 'Only a review waiting for you can be dragged to Done. To close a ticket, cancel it.' },
      { label: 'Move to top', disabled: false, hint: 'Top of the tasks in Planning.' },
      { label: 'Move to bottom', disabled: true, hint: 'It is at the bottom already.' },
    ])
  })

  test('a busy card names the busy reason; a Done card offers nothing and no reordering', async () => {
    const busy = ticket({ number: 4, column: 'in_progress', busy: true, agents: [{ session: 's', role: 'developer', name: 'dev', state: 'running', purpose: 'work' }] })
    const items = await open(menu(busy))
    expect(items.find((i) => i.label === 'Planning')).toEqual({ label: 'Planning', disabled: true, hint: 'omo is starting work on or merging #4; try again in a moment.' })
    mounted!.unmount()

    const done = ticket({ number: 2, column: 'done', resolution: 'completed', closed_at: '2026-11-15T10:00:00Z' })
    const doneItems = await open(menu(done))
    expect(doneItems.map((i) => i.label)).toEqual(['Planning', 'Ready For Dev', 'In Progress', 'Review', 'Done'])
    expect(doneItems.every((i) => i.disabled && i.hint === 'Done tickets stay where they are.')).toBe(true)
  })

  test('each item emits the move with the place a drop would take', async () => {
    const story = ticket({ number: 1, type: 'story', rank: 10, projects: [on('api')] })
    const all = [story, ticket({ number: 2, type: 'story', rank: 20 }), ticket({ number: 3, rank: 30 }), ticket({ number: 4, column: 'ready', type: 'story', rank: 1 }), ticket({ number: 5, column: 'ready', rank: 2 })]
    const w = menu(story, all)
    await open(w)
    const pick = async (label: string) => {
      const items = w.findAll('[role="menuitem"]')
      await items.find((i) => i.get('.menu__label').text() === label)!.trigger('click')
      await nextTick()
      await w.get('button').trigger('keydown', { key: 'ArrowDown' })
      await nextTick()
    }
    await pick('Ready For Dev')
    await pick('Move to bottom')
    const sent = (w.emitted('move') as [Target, number | null][]).map(([t, b]) => [t.column, t.action, b])
    // To the end of the stories in Ready For Dev, above the first task; to the end of the stories in Planning, above #3.
    expect(sent).toEqual([
      ['ready', 'approve', 5],
      ['planning', 'reorder', 3],
    ])
  })

  test('on the board, choosing a column moves the card and the focus stays on it', async () => {
    const h: Harness = await harness((omo) => {
      omo.tickets = [ticket({ number: 1, projects: [on('api')] })]
      omo.projects = [project('api')]
    })
    h.omo.stream!.onState('live')
    h.respond.write = () => new Promise(() => {})
    mounted = await mountWith(h, LiveBoard, {})
    const card = () => document.querySelector<HTMLElement>('li[data-ticket="1"]')!
    const trigger = card().querySelector<HTMLElement>('[data-move-menu] button')!
    trigger.focus()
    trigger.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    await nextTick()
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    await nextTick()
    // Ready For Dev is the second item.
    const menuEl = card().querySelector<HTMLElement>('[role="menu"]')!
    menuEl.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    await nextTick()
    expect(document.activeElement?.textContent).toContain('Ready For Dev')
    menuEl.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await flushPromises()
    expect(h.sent.filter((s) => s.method === 'POST')).toEqual([{ method: 'POST', path: '/api/tickets/1/move', body: { column: 'ready', before: null } }])
    expect(card().closest('section')!.dataset.dropColumn).toBe('ready')
    expect(card().querySelector('[data-card-link]')).toBe(document.activeElement)
  })
})

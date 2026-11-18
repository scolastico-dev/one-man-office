import { flushPromises, mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import type { TicketSummary } from '../../api/types'
import { createBoardStore, type BoardStore } from '../../store'
import { FakeOmo, project } from '../../testing/fakeOmo'
import type { Filters } from '../filters/useFilters'
import BoardView from './BoardView.vue'
import { ticket } from './testing'
import { useBoardColumns } from './useBoardColumns'

const on = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '', export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '' })

async function storeWith(tickets: TicketSummary[], setup?: (omo: FakeOmo) => void): Promise<{ omo: FakeOmo; store: BoardStore }> {
  const omo = new FakeOmo()
  omo.tickets = tickets
  setup?.(omo)
  const store = createBoardStore({ client: omo.makeClient({ getToken: () => null, forgetToken: () => null, onUnauthorized: () => {} }), connect: omo.connect })
  await store.start()
  omo.stream!.onState('live')
  return { omo, store }
}

function noFilter() {
  return { filters: ref<Filters>({}), serverHits: ref<Set<number> | undefined>(undefined) }
}

test('open columns list stories, then tasks, each by rank; groups hold the two kinds', async () => {
  const { store } = await storeWith([
    ticket({ number: 1, rank: 3 }),
    ticket({ number: 2, rank: 1, type: 'story' }),
    ticket({ number: 3, rank: 2 }),
    ticket({ number: 4, rank: 1, column: 'review', review_stage: 'agent' }),
  ])
  const cols = useBoardColumns({ store, filters: noFilter() }).value
  expect(cols.map((c) => c.title)).toEqual(['Planning', 'Ready For Dev', 'In Progress', 'Review', 'Done'])
  const planning = cols[0]!
  expect(planning.cards.map((c) => c.ticket.number)).toEqual([2, 3, 1])
  expect(planning.groups!.stories.map((c) => c.ticket.number)).toEqual([2])
  expect(planning.groups!.tasks.map((c) => c.ticket.number)).toEqual([3, 1])
  expect(planning.count).toBe(3)
  expect(cols[3]!.cards[0]!.caption).toBe('agent review')
})

test('Done is ordered by closing time, has no groups, and offers older pages', async () => {
  const { store } = await storeWith(
    [
      ticket({ number: 1, column: 'done', closed_at: '2026-11-15T10:00:05Z' }),
      ticket({ number: 2, column: 'done', closed_at: '2026-11-15T10:00:05.5Z' }),
      ticket({ number: 3, column: 'done', type: 'story', closed_at: '2026-11-15T10:00:04Z' }),
    ],
    (omo) => (omo.donePage = 2),
  )
  const done = useBoardColumns({ store, filters: noFilter() }).value[4]!
  expect(done.cards.map((c) => c.ticket.number)).toEqual([2, 1])
  expect(done.groups).toBeUndefined()
  expect(done.more).toBe(true)
})

test('cards name their projects (the id when unknown) and their story (the number when not loaded)', async () => {
  const { store } = await storeWith(
    [
      ticket({ number: 1, type: 'story', title: 'The story' }),
      ticket({ number: 2, story: 1, projects: [on('ui'), on('gone')] }),
      ticket({ number: 3, story: 99 }),
    ],
    (omo) => (omo.projects = [project('ui', 'acme-corp/ui')]),
  )
  const cards = useBoardColumns({ store, filters: noFilter() }).value[0]!.cards
  const two = cards.find((c) => c.ticket.number === 2)!
  expect(two.projectNames).toEqual(['acme-corp/ui', 'gone'])
  expect(two.storyTitle).toBe('The story')
  expect(cards.find((c) => c.ticket.number === 3)!.storyTitle).toBeUndefined()
})

test('filters hide cards and the counts follow', async () => {
  const { store } = await storeWith([ticket({ number: 1, title: 'alpha' }), ticket({ number: 2, title: 'beta' })])
  const f = noFilter()
  const cols = useBoardColumns({ store, filters: f })
  f.filters.value = { q: 'beta' }
  expect(cols.value[0]!.cards.map((c) => c.ticket.number)).toEqual([2])
  expect(cols.value[0]!.count).toBe(1)
})

test('a change to one ticket of 300 rebuilds one card model and renders one card again', async () => {
  const columns = ['planning', 'ready', 'in_progress', 'review', 'done'] as const
  const tickets = Array.from({ length: 300 }, (_, i) =>
    ticket({ number: i + 1, rank: i + 1, column: columns[i % 5]!, closed_at: i % 5 === 4 ? '2026-11-15T10:00:00Z' : null }),
  )
  const { omo, store } = await storeWith(tickets, (o) => (o.donePage = 100))
  const cols = useBoardColumns({ store, filters: noFilter() })
  const before = new Map(cols.value.flatMap((c) => c.cards).map((c) => [c.ticket.number, c]))
  expect(before.size).toBe(300)

  let renders = 0
  const Host = defineComponent({
    setup: () => () => h(BoardView, { columns: cols.value }),
  })
  const wrapper = mount(Host, {
    global: {
      mixins: [
        {
          beforeUpdate(this: { $options: { __name?: string } }) {
            if (this.$options.__name === 'TicketCard') renders++
          },
        },
      ],
    },
  })

  omo.change('ticket.updated', ticket({ ...tickets[6]!, title: 'Renamed' }))
  await flushPromises()

  const after = cols.value.flatMap((c) => c.cards)
  const rebuilt = after.filter((c) => before.get(c.ticket.number) !== c).map((c) => c.ticket.number)
  expect(rebuilt).toEqual([7])
  expect(renders).toBe(1)
  expect(wrapper.get('[data-ticket="7"]').text()).toContain('Renamed')
  wrapper.unmount()
})

test('a pending move shows the card in its target column at once', async () => {
  const { store } = await storeWith([ticket({ number: 1, projects: [] })])
  const cols = useBoardColumns({ store, filters: noFilter() })
  const moving = store.requestMove(1, 'ready', null).catch(() => undefined)
  expect(cols.value[1]!.cards.map((c) => [c.ticket.number, c.pending])).toEqual([[1, true]])
  expect(cols.value[0]!.cards).toEqual([])
  await moving
  await flushPromises()
  expect(cols.value[0]!.cards.map((c) => c.ticket.number)).toEqual([1])
})


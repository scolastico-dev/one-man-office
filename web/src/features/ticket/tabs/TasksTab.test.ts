import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { ticket } from '../../board/testing'
import { project } from '../../../testing/fakeOmo'
import { useCreateTicket } from '../../create/useCreateTicket'
import { mountPanel, ok, panelHarness } from '../panelTesting'
import TasksTab from './TasksTab.vue'

let mounted: VueWrapper | null = null
beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }))
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.useRealTimers()
  useCreateTicket().close()
})

test('tasks tab lists open and done tasks with columns and does not change the board store', async () => {
  const story = ticket({ number: 12, type: 'story', title: 'The story' })
  const open1 = ticket({ number: 13, story: 12, title: 'First <b>task</b>', column: 'ready' })
  const open2 = ticket({ number: 14, story: 12, title: 'Second', column: 'in_progress', depends_on: [13], questions_for_user: 1 })
  const doneTask = ticket({ number: 15, story: 12, title: 'Finished', column: 'done' })
  const hn = await panelHarness((omo) => {
    omo.projects = [project('demo')]
    omo.tickets = [story, open1, open2]
  })
  const queries: unknown[] = []
  const get = hn.session.client.get
  hn.session.client.get = (async (path: string, q?: never) => {
    if (path === '/api/tickets') {
      queries.push(q)
      return ok({ tickets: [open1, doneTask, ticket({ number: 40, story: 3, column: 'done' })], event_id: 1 })
    }
    return get(path, q)
  }) as typeof get
  const known = [...hn.session.store.state.tickets.keys()].sort()

  const { wrapper: w } = await mountPanel(hn, TasksTab, { ticket: story })
  mounted = w
  await flushPromises()

  expect(queries).toEqual([{ story: 12, done_limit: 500 }])
  expect(w.findAll('[data-task]').map((r) => r.attributes('data-task'))).toEqual(['13', '14', '15'])
  expect(w.findAll('[data-task-column]').map((c) => c.text())).toEqual(['Ready For Dev', 'In Progress', 'Done'])
  expect(w.get('[data-task="13"]').text()).toContain('First <b>task</b>')
  expect(w.find('b').exists()).toBe(false)
  expect(w.get('[data-task="13"] a').attributes('href')).toBe('/ticket/13')
  expect(w.get('[data-task="14"]').text()).toContain('question for you')
  expect(w.get('[data-task="14"]').text()).toContain('depends on #13')
  expect(w.get('[data-task="15"]').text()).toContain('Finished')
  // The Done task lives in this tab only.
  expect([...hn.session.store.state.tickets.keys()].sort()).toEqual(known)

  // A task that reaches Done moves to the read list.
  hn.omo.change('ticket.moved', { ...open1, column: 'done' })
  await flushPromises()
  // Until the read answers, the task stays in the list, as Done.
  expect(w.findAll('[data-task]').map((r) => r.attributes('data-task'))).toEqual(['14', '13', '15'])
  expect(w.get('[data-task="13"] [data-task-column]').text()).toBe('Done')
  vi.advanceTimersByTime(500)
  await flushPromises()
  expect(queries).toHaveLength(2)
  expect(w.findAll('[data-task]').map((r) => r.attributes('data-task'))).toEqual(['14', '13', '15'])

  await w.get('[data-add-task]').trigger('click')
  expect(useCreateTicket().state.open).toBe(true)
  expect(useCreateTicket().state.initialStory).toBe(12)
})

test('tasks tab: a story with no tasks says so', async () => {
  const story = ticket({ number: 12, type: 'story' })
  const hn = await panelHarness((omo) => {
    omo.tickets = [story]
  })
  const get = hn.session.client.get
  hn.session.client.get = (async (path: string, q?: never) => (path === '/api/tickets' ? ok({ tickets: [], event_id: 1 }) : get(path, q))) as typeof get
  const { wrapper: w } = await mountPanel(hn, TasksTab, { ticket: story })
  mounted = w
  await flushPromises()
  expect(w.get('[data-tasks-empty]').text()).toBe('This story has no tasks yet.')
})

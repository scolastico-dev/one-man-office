import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'
import App from '../../App.vue'
import type { ApiClient, ClientOptions, Query } from '../../api/client'
import type { TicketList, TicketSummary } from '../../api/types'
import { createTokenStore } from '../../auth/token'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { createAppRouter } from '../../router'
import { createSession, SESSION } from '../../session'
import { FakeOmo, MemoryStorage, project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'

let mounted: VueWrapper | null = null
beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }))
afterEach(() => {
  vi.useRealTimers()
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

const on = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' })

function omoWithBoard(): FakeOmo {
  const omo = new FakeOmo()
  omo.projects = [project('ui', 'not-three/ui'), project('api', 'not-three/api')]
  omo.tickets = [
    ticket({ number: 1, type: 'story', title: 'Story one', rank: 1 }),
    ticket({ number: 2, title: 'Task two', rank: 2, story: 1, projects: [on('ui')] }),
    ticket({ number: 3, title: 'Alone', rank: 3, projects: [on('api')] }),
    ticket({ number: 4, title: 'Needs review', column: 'review', review_stage: 'human', rank: 1, projects: [on('ui')] }),
    ticket({ number: 5, title: 'Stuck', column: 'in_progress', attention: 'agent keeps failing', rank: 1, supervisor: 'user' }),
    ticket({ number: 6, title: 'Asks', column: 'ready', questions_for_user: 1, open_questions: 1, rank: 1, supervisor: 'ceo' }),
  ]
  return omo
}

interface Search {
  queries: string[]
  /** Answers a search; defaults to no tickets. */
  answer: (q: string) => Promise<TicketSummary[]> | TicketSummary[]
}

async function boot(omo: FakeOmo, search?: Search, path = '/') {
  const storage = new MemoryStorage()
  const makeClient = (o: ClientOptions): ApiClient => {
    const c = omo.makeClient(o)
    return {
      ...c,
      get: async <T,>(p: string, query?: Query) => {
        if (p === '/api/tickets' && query?.q !== undefined && search) {
          search.queries.push(String(query.q))
          const tickets = await search.answer(String(query.q))
          return { data: { tickets, event_id: omo.eventId } satisfies TicketList as T, eventId: null }
        }
        return c.get<T>(p, query)
      },
    }
  }
  const router = createAppRouter({ history: createMemoryHistory() })
  await router.push(path)
  await router.isReady()
  const session = createSession({ tokens: createTokenStore(() => storage), makeClient, connect: omo.connect })
  const wrapper = mount(App, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: session } } })
  mounted = wrapper
  await flushPromises()
  return { wrapper, router, session }
}

/** Chooses a project in the project filter's search. */
async function pickProject(w: VueWrapper, id: string) {
  await w.get('[data-filter-project] button').trigger('click')
  await w.get(`[data-filter-project] [data-pick-item="${id}"]`).trigger('click')
  await flushPromises()
}

const cards = (w: VueWrapper) => w.findAll('[data-board] [data-ticket]').map((c) => Number(c.attributes('data-ticket'))).sort((a, b) => a - b)

test('filters round-trip through the URL query', async () => {
  const { wrapper, router } = await boot(omoWithBoard(), undefined, '/?project=ui&needs_me=1')
  expect(cards(wrapper)).toEqual([4])
  expect(wrapper.get('[data-filter-project] button').attributes('aria-label')).toBe('Project: not-three/ui')

  // Clearing writes the address; other query parts and the path stay.
  await wrapper.get('[data-clear-filters]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({})
  expect(cards(wrapper)).toEqual([1, 2, 3, 4, 5, 6])

  const supervisor = wrapper.findAll('select')[1]!
  await supervisor.setValue('ceo')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ supervisor: 'ceo' })
  expect(cards(wrapper)).toEqual([6])

  // The address changes (back button, shared link): the board follows.
  await router.push('/?story=1&q=two')
  await flushPromises()
  expect(cards(wrapper)).toEqual([2])
  expect((wrapper.get('input[type=search]').element as HTMLInputElement).value).toBe('two')
})

test('clicking a story chip filters the board to the story and its tasks', async () => {
  const { wrapper, router } = await boot(omoWithBoard())
  await wrapper.get('[data-ticket="2"] [data-chip="story"]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ story: '1' })
  expect(cards(wrapper)).toEqual([1, 2])
  expect(wrapper.get('[data-story-chip]').attributes('aria-label')).toContain('#1 Story one')

  await wrapper.get('[data-story-chip]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({})
  expect(cards(wrapper)).toEqual([1, 2, 3, 4, 5, 6])
})

test('needs-me count equals the number of matching cards', async () => {
  const { wrapper } = await boot(omoWithBoard())
  const count = () => wrapper.get('[data-needs-me-count]').text()
  expect(count()).toBe('3')
  await wrapper.get('button[aria-pressed]').trigger('click')
  await flushPromises()
  expect(cards(wrapper)).toEqual([4, 5, 6])
  expect(Number(count())).toBe(cards(wrapper).length)
  // The count follows the other filters.
  await pickProject(wrapper, 'ui')
  expect(cards(wrapper)).toEqual([4])
  expect(count()).toBe('1')
})

test('columns show the filtered count and the total', async () => {
  const { wrapper } = await boot(omoWithBoard(), undefined, '/?project=ui')
  expect(wrapper.get('[data-drop-column="planning"] h2').text().replace(/\s+/g, ' ')).toBe('Planning 1 of 3')
  expect(wrapper.get('[data-drop-column="in_progress"] h2').text().replace(/\s+/g, ' ')).toBe('In Progress 0 of 1')
})

test('a filter that hides everything says so and can be cleared; the first steps stay away', async () => {
  const { wrapper } = await boot(omoWithBoard(), undefined, '/?q=zzzz')
  expect(cards(wrapper)).toEqual([])
  expect(wrapper.find('[data-nothing-matches]').exists()).toBe(true)
  expect(wrapper.text()).not.toContain('first steps')
  await wrapper.get('[data-nothing-matches] button').trigger('click')
  await flushPromises()
  expect(cards(wrapper).length).toBe(6)
  expect(wrapper.find('[data-nothing-matches]').exists()).toBe(false)
})

test('search shows server hits for words only in a description', async () => {
  const omo = omoWithBoard()
  const hit = ticket({ number: 9, title: 'Closed long ago', column: 'done', closed_at: '2026-11-01T10:00:00Z' })
  const search: Search = { queries: [], answer: () => [hit] }
  const { wrapper } = await boot(omo, search)

  await wrapper.get('input[type=search]').setValue('migration')
  await flushPromises()
  // Local matching is instant and finds nothing; the server has not been asked yet.
  expect(cards(wrapper)).toEqual([])
  expect(search.queries).toEqual([])
  expect(wrapper.get('[data-search-status]').text()).toBe('Searching…')

  vi.advanceTimersByTime(250)
  await flushPromises()
  expect(search.queries).toEqual(['migration'])
  expect(cards(wrapper)).toEqual([9])
  expect(wrapper.get('[data-search-status]').text()).toBe('')
})

test('typing waits for a pause before the server is asked, and a title match shows at once', async () => {
  const search: Search = { queries: [], answer: () => [] }
  const { wrapper } = await boot(omoWithBoard(), search)
  const input = wrapper.get('input[type=search]')
  await input.setValue('al')
  vi.advanceTimersByTime(200)
  await input.setValue('alo')
  expect(cards(wrapper)).toEqual([3])
  vi.advanceTimersByTime(200)
  expect(search.queries).toEqual([])
  vi.advanceTimersByTime(60)
  await flushPromises()
  expect(search.queries).toEqual(['alo'])
})

test('a server hit beyond the loaded Done pages shows while searching and is never put into the store', async () => {
  const omo = omoWithBoard()
  const hit = ticket({ number: 9, title: 'Closed long ago', column: 'done', closed_at: '2026-11-01T10:00:00Z' })
  const search: Search = { queries: [], answer: () => [hit] }
  const { wrapper, session } = await boot(omo, search)
  const doneTotal = () => session.store.columns.value.done.all.length
  const before = doneTotal()

  await wrapper.get('input[type=search]').setValue('migration')
  vi.advanceTimersByTime(250)
  await flushPromises()
  expect(cards(wrapper)).toEqual([9])
  // Shown, but the store (and with it the Done total and the paging) knows nothing of it.
  expect(session.store.view(9)).toBeUndefined()
  expect(doneTotal()).toBe(before)

  await wrapper.get('input[type=search]').setValue('')
  await flushPromises()
  expect(session.store.view(9)).toBeUndefined()
  expect(doneTotal()).toBe(before)
  expect(cards(wrapper)).not.toContain(9)
})

test('the answer to an older search never shows after a newer one', async () => {
  let release!: (t: TicketSummary[]) => void
  const slow = new Promise<TicketSummary[]>((r) => (release = r))
  const search: Search = { queries: [], answer: (q) => (q === 'old' ? slow : [ticket({ number: 8, title: 'Newer hit', column: 'done', closed_at: '2026-11-01T10:00:00Z' })]) }
  const { wrapper } = await boot(omoWithBoard(), search)
  const input = wrapper.get('input[type=search]')

  await input.setValue('old')
  vi.advanceTimersByTime(250)
  await flushPromises()
  await input.setValue('new')
  vi.advanceTimersByTime(250)
  await flushPromises()
  expect(cards(wrapper)).toEqual([8])

  release([ticket({ number: 7, title: 'Older hit', column: 'done', closed_at: '2026-11-01T10:00:00Z' })])
  await flushPromises()
  expect(cards(wrapper)).toEqual([8])
})

test('a failing server search says so and keeps the local matches', async () => {
  const search: Search = {
    queries: [],
    answer: () => {
      throw new Error('boom')
    },
  }
  const { wrapper } = await boot(omoWithBoard(), search)
  await wrapper.get('input[type=search]').setValue('alone')
  vi.advanceTimersByTime(250)
  await flushPromises()
  expect(cards(wrapper)).toEqual([3])
  expect(wrapper.get('[data-search-status]').text()).toContain('could not search descriptions')
})

test('the project filter searches the projects, keeps All projects first and writes the address as before', async () => {
  const { wrapper, router } = await boot(omoWithBoard())
  const picker = wrapper.get('[data-filter-project]')
  expect(picker.get('button').attributes('aria-label')).toBe('Project: All projects')
  await picker.get('button').trigger('click')
  const search = picker.get('input[role="combobox"]')
  expect(search.attributes('aria-label')).toBe('Search projects')
  await search.setValue('zzz')
  expect(picker.findAll('[data-pick-item]').map((i) => i.attributes('data-pick-item'))).toEqual([''])
  await search.setValue('/ui')
  await search.trigger('keydown', { key: 'ArrowDown' })
  await search.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ project: 'ui' })
  expect(cards(wrapper)).toEqual([2, 4])
  expect(picker.get('button').text()).toBe('not-three/ui')

  await pickProject(wrapper, '')
  expect(router.currentRoute.value.query).toEqual({})
})

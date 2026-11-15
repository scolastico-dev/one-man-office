import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { defineComponent } from 'vue'
import { ApiError } from '../../api/client'
import type { ApiEvent, Comment, TicketThread } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import { buildThread } from './thread'
import { useThread, type Thread } from './useThread'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

function comment(n: number, o: Partial<Comment> = {}): Comment {
  return { id: n, ticket: 4, author: { role: 'developer', name: 'Ada' }, body: `c${n}`, reply_to: null, question_to: '' as Comment['question_to'], answered_by: null, mentions: [], created_at: '2026-11-15T10:00:00.000Z', ...o }
}
const created = (id: number, c: Comment = comment(id)): ApiEvent => ({ id, kind: 'comment.created', ticket: 4, actor: c.author, at: '2026-11-15T10:00:00.000Z', data: { comment: c } })

interface Fixture {
  hn: PanelHarness
  thread: Thread
  asked: Array<{ before?: unknown; limit?: unknown }>
  /** What the thread route answers next. */
  page: { fn: (q: { before?: unknown }) => TicketThread | Promise<TicketThread> }
  live(ev: ApiEvent): void
}

async function setup(first: TicketThread): Promise<Fixture> {
  const hn = await panelHarness((omo) => {
    omo.tickets = [ticket({ number: 4 })]
    omo.projects = [project('demo')]
  })
  const asked: Fixture['asked'] = []
  const page: Fixture['page'] = { fn: () => first }
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = ((path: string, query?: Record<string, unknown>) => {
    if (path === '/api/tickets/4/thread') {
      asked.push({ before: query?.before, limit: query?.limit })
      return Promise.resolve(page.fn({ before: query?.before })).then((t) => ok(t))
    }
    return get(path, query as never)
  }) as typeof client.get
  let thread!: Thread
  const Host = defineComponent({
    setup() {
      thread = useThread(4)
      return () => null
    },
  })
  const { wrapper } = await mountPanel(hn, Host, {})
  mounted = wrapper
  await flushPromises()
  return { hn, thread, asked, page, live: (ev) => hn.omo.stream!.onEvent(ev) }
}

const ids = (t: Thread) => t.items.value.map((i) => (i.kind === 'comment' ? `c${i.comment.id}` : `e${i.eventId}`))

test('the first page shows; live events append once; replayed events change nothing', async () => {
  const f = await setup({ events: [created(1001), created(1002)], more: false })
  expect(ids(f.thread)).toEqual(['c1001', 'c1002'])
  f.live(created(1003))
  f.live(created(1003))
  f.live({ ...created(1004), ticket: 5 })
  f.live({ id: 1005, kind: 'ticket.updated', ticket: 4, actor: { role: 'user' }, at: '', data: {} })
  expect(ids(f.thread)).toEqual(['c1001', 'c1002', 'c1003'])
})

test('Load older reads by cursor, joins without gaps and stops at the end', async () => {
  const f = await setup({ events: [created(1003), created(1004)], more: true })
  expect(f.thread.more.value).toBe(true)
  f.page.fn = (q) => {
    expect(q.before).toBe(1003)
    return { events: [created(1001), created(1002)], more: true }
  }
  await f.thread.loadOlder()
  expect(ids(f.thread)).toEqual(['c1001', 'c1002', 'c1003', 'c1004'])
  expect(f.thread.more.value).toBe(true)
  f.page.fn = (q) => {
    expect(q.before).toBe(1001)
    return { events: [], more: false }
  }
  await f.thread.loadOlder()
  expect(f.thread.more.value).toBe(false)
  expect(f.asked.map((a) => a.before)).toEqual([undefined, 1003, 1001])
})

test('a resync reads the newest page again and merges it; nothing is lost or doubled', async () => {
  const f = await setup({ events: [created(1001), created(1002)], more: false })
  f.live(created(1003))
  f.page.fn = () => ({ events: [created(1002), created(1003), created(1004)], more: false })
  f.hn.omo.stream!.onResync(f.hn.omo.eventId)
  await flushPromises()
  await f.thread.refresh()
  expect(ids(f.thread)).toEqual(['c1001', 'c1002', 'c1003', 'c1004'])
})

test('a reread whose page is entirely newer than everything known starts from the page', async () => {
  const f = await setup({ events: [created(1001)], more: false })
  f.page.fn = () => ({ events: [created(1500), created(1501)], more: true })
  await f.thread.refresh()
  expect(ids(f.thread)).toEqual(['c1500', 'c1501'])
  expect(f.thread.more.value).toBe(true)
})

test('an older page that answers after the thread started again from a newer page is dropped', async () => {
  const f = await setup({ events: [created(1003), created(1004)], more: true })
  let answer!: (t: TicketThread) => void
  f.page.fn = () => new Promise<TicketThread>((resolve) => (answer = resolve))
  const older = f.thread.loadOlder()
  // Events were missed meanwhile: the newest page is entirely newer than what is known.
  f.page.fn = () => ({ events: [created(2001), created(2002)], more: true })
  await f.thread.refresh()
  expect(ids(f.thread)).toEqual(['c2001', 'c2002'])
  answer({ events: [created(1001), created(1002)], more: false })
  await older
  await flushPromises()
  expect(ids(f.thread)).toEqual(['c2001', 'c2002'])
  expect(f.thread.more.value).toBe(true)
  expect(f.thread.loadingOlder.value).toBe(false)
})

test('a posted comment shows once, from the answer or the stream, whichever is first', async () => {
  const f = await setup({ events: [created(1001)], more: false })
  const mine = comment(1010, { author: { role: 'user' } })
  f.hn.write.fn = (s) => {
    expect(s.path).toBe('/api/tickets/4/comments')
    return ok({ comment: mine, unreachable: ['reviewer'] }, 1010)
  }
  const unreachable = await f.thread.post({ body: 'c1010', question: false })
  expect(unreachable).toEqual(['reviewer'])
  expect(ids(f.thread)).toEqual(['c1001', 'c1010'])
  expect(f.thread.items.value[1]).toMatchObject({ sending: true })
  // The stream's copy replaces the answer's: still once.
  f.live(created(1010, mine))
  expect(ids(f.thread)).toEqual(['c1001', 'c1010'])
  expect(f.thread.items.value[1]).not.toHaveProperty('sending')
  // Stream first, then the answer.
  const other = comment(1011, { author: { role: 'user' } })
  f.hn.write.fn = () => {
    f.live(created(1011, other))
    return ok({ comment: other, unreachable: [] }, 1011)
  }
  await f.thread.post({ body: 'c1011', question: false })
  expect(ids(f.thread)).toEqual(['c1001', 'c1010', 'c1011'])
})

test('a lost answer rejects with the network error and reads the thread again', async () => {
  const f = await setup({ events: [created(1001)], more: false })
  f.hn.write.fn = () => {
    f.page.fn = () => ({ events: [created(1001), created(1020, comment(1020, { author: { role: 'user' } }))], more: false })
    throw new ApiError(0, 'network', 'Cannot reach omo.')
  }
  await expect(f.thread.post({ body: 'x', question: false })).rejects.toMatchObject({ code: 'network' })
  await flushPromises()
  expect(ids(f.thread)).toEqual(['c1001', 'c1020'])
})

test('the built items follow the log', async () => {
  const f = await setup({ events: [created(1001), { id: 1002, kind: 'question.answered', ticket: 4, actor: { role: 'user' }, at: '', data: { ticket: 4, question: 1001, answer: 1003 } }], more: false })
  expect(buildThread([]).length).toBe(0)
  expect(f.thread.items.value[0]).toMatchObject({ kind: 'comment', answered: true, answeredBy: 1003 })
})

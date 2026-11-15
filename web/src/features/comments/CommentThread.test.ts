import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { defineComponent, h } from 'vue'
import type { ApiEvent, Comment, TicketThread } from '../../api/types'
import { announcements } from '../../components/ui/useAnnouncer'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { useNotifications } from '../notifications/useNotifications'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import CommentThread from './CommentThread.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

function comment(id: number, o: Partial<Comment> = {}): Comment {
  return { id, ticket: 4, author: { role: 'developer', name: 'Ada' }, body: `c${id}`, reply_to: null, question_to: '' as Comment['question_to'], answered_by: null, mentions: [], created_at: '2026-11-15T10:00:00.000Z', ...o }
}
const created = (id: number, c: Comment = comment(id)): ApiEvent => ({ id: 1000 + id, kind: 'comment.created', ticket: 4, actor: c.author, at: '2026-11-15T10:00:00.000Z', data: { comment: c } })

interface Fixture {
  hn: PanelHarness
  wrapper: VueWrapper
  /** What the thread route answers, by `before`. */
  page: { fn: (before: unknown) => TicketThread | Promise<TicketThread> }
  asked: unknown[]
}

async function setup(first: TicketThread, path = '/4', withBell = false): Promise<Fixture> {
  const hn = await panelHarness((omo) => {
    omo.tickets = [ticket({ number: 4, title: 'Open one' })]
    omo.projects = [project('demo')]
  })
  const asked: unknown[] = []
  const page: Fixture['page'] = { fn: () => first }
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = ((p: string, query?: Record<string, unknown>) => {
    if (p === '/api/tickets/4/thread') {
      asked.push(query?.before)
      return Promise.resolve(page.fn(query?.before)).then((t) => ok(t))
    }
    if (p === '/api/notifications') return Promise.resolve(ok({ mentions: [], seen_through: 0, unread: 0 }))
    return get(p, query as never)
  }) as typeof client.get
  const Host = defineComponent({
    setup() {
      if (withBell) useNotifications()
      return () => h(CommentThread, { ticket: hn.omo.tickets[0]! })
    },
  })
  const { wrapper } = await mountPanel(hn, Host, {}, path)
  mounted = wrapper
  await flushPromises()
  return { hn, wrapper, page, asked }
}

test('several entries arriving at once are all announced', async () => {
  const f = await setup({ events: [created(1)], more: false })
  f.hn.omo.stream!.onEvent(created(2, comment(2, { author: { role: 'developer', name: 'Ada' } })))
  f.hn.omo.stream!.onEvent(created(3, comment(3, { author: { role: 'reviewer', name: 'Bo' } })))
  await flushPromises()
  expect(announcements.polite.value).toBe('New comment from developer Ada. New comment from reviewer Bo.')
})

test('a mention of the user on the open ticket is announced once, by the bell', async () => {
  const f = await setup({ events: [created(1)], more: false }, '/4', true)
  f.hn.omo.stream!.onEvent(created(2, comment(2, { body: '@user look', mentions: [{ role: 'user' }] })))
  await flushPromises()
  expect(announcements.polite.value).toBe('New mention on #4 from developer Ada')
  f.hn.omo.stream!.onEvent(created(3, comment(3, { body: 'Which one?', question_to: 'user' })))
  await flushPromises()
  expect(announcements.polite.value).toBe('New question for you on #4 from developer Ada')
})

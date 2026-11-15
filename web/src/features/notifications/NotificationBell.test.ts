import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import type { Router } from 'vue-router'
import type { Comment, MentionNotice, Notifications, TicketSummary } from '../../api/types'
import { announcements } from '../../components/ui/useAnnouncer'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import NotificationBell from './NotificationBell.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.unstubAllGlobals()
})

function comment(id: number, o: Partial<Comment> = {}): Comment {
  return {
    id,
    ticket: 4,
    author: { role: 'developer', name: 'calm-badger' },
    body: `@user c${id}`,
    reply_to: null,
    question_to: '' as Comment['question_to'],
    answered_by: null,
    mentions: [{ role: 'user' }],
    created_at: '2026-11-15T10:00:00.000Z',
    ...o,
  }
}
const notice = (id: number, unread: boolean, o: Partial<Comment> = {}, title = 'Drag a card'): MentionNotice => ({ comment: comment(id, o), title, unread })

interface Fixture {
  hn: PanelHarness
  wrapper: VueWrapper
  router: Router
  marks: number[]
  threadReads: number[]
}

async function setup(tickets: TicketSummary[], first: Notifications): Promise<Fixture> {
  const hn = await panelHarness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  })
  const client = hn.session.client
  const get = client.get.bind(client)
  const threadReads: number[] = []
  client.get = ((path: string, query?: Record<string, unknown>) => {
    if (path === '/api/notifications') return Promise.resolve(ok(first))
    const m = /^\/api\/tickets\/(\d+)\/thread$/.exec(path)
    if (m) {
      threadReads.push(Number(m[1]))
      const q = comment(77, { ticket: Number(m[1]), question_to: 'user', mentions: [] })
      return Promise.resolve(ok({ events: [{ id: 500, kind: 'comment.created', ticket: Number(m[1]), actor: q.author, at: q.created_at, data: { comment: q } }], more: false }))
    }
    return get(path, query as never)
  }) as typeof client.get
  const marks: number[] = []
  hn.write.fn = (s) => {
    const through = (s.body as { through: number }).through
    marks.push(through)
    return ok({ ...first, seen_through: through, unread: 0 })
  }
  const { wrapper, router } = await mountPanel(hn, NotificationBell, {})
  mounted = wrapper
  await flushPromises()
  return { hn, wrapper, router, marks, threadReads }
}

const bell = (w: VueWrapper) => w.get('[data-bell]')
const list = (w: VueWrapper) => w.find('[data-bell-list]')

test('the button names the counts and opens a labelled list with both groups', async () => {
  const f = await setup([ticket({ number: 13, title: 'Asks', column: 'in_progress', questions_for_user: 1 }), ticket({ number: 9, title: 'Review', column: 'review', review_stage: 'human' })], {
    mentions: [notice(121, true), notice(97, false)],
    seen_through: 100,
    unread: 1,
  })
  expect(bell(f.wrapper).attributes('aria-label')).toBe('Notifications: 2 waiting for you, 1 unread mention')
  expect(bell(f.wrapper).attributes('aria-expanded')).toBe('false')
  expect(f.wrapper.get('[data-bell-count]').text()).toBe('3')
  expect(list(f.wrapper).exists()).toBe(false)

  await bell(f.wrapper).trigger('click')
  expect(bell(f.wrapper).attributes('aria-expanded')).toBe('true')
  const region = list(f.wrapper)
  expect(region.attributes('role')).toBe('region')
  expect(region.attributes('aria-label')).toBe('Notifications')
  expect(region.findAll('[data-waiting]').map((w) => w.text())).toEqual([expect.stringContaining('#13'), expect.stringContaining('#9')])
  const mentions = region.findAll('[data-mention]')
  expect(mentions).toHaveLength(2)
  expect(mentions[0]!.attributes('data-unread')).toBe('true')
  expect(mentions[0]!.text()).toContain('Unread')
  expect(mentions[1]!.attributes('data-unread')).toBe('false')
})

test('Escape closes the list and gives the focus back to the bell', async () => {
  const f = await setup([], { mentions: [], seen_through: 0, unread: 0 })
  expect(f.wrapper.find('[data-bell-count]').exists()).toBe(false)
  await bell(f.wrapper).trigger('click')
  expect(list(f.wrapper).text()).toContain('Nothing waits for you.')
  await list(f.wrapper).trigger('keydown', { key: 'Escape' })
  expect(list(f.wrapper).exists()).toBe(false)
  expect(document.activeElement).toBe(bell(f.wrapper).element)
})

test('agent text is shown as text: no markup, no reordering characters', async () => {
  const f = await setup([], {
    mentions: [notice(5, true, { body: '<img src=x onerror=alert(1)> @user\nsee‮exe.txt', author: { role: 'developer', name: '<b>bad</b>' } }, '<script>t</script>')],
    seen_through: 0,
    unread: 1,
  })
  await bell(f.wrapper).trigger('click')
  const m = list(f.wrapper).get('[data-mention]')
  expect(m.find('img').exists()).toBe(false)
  expect(m.find('b').exists()).toBe(false)
  expect(m.text()).toContain('<img src=x onerror=alert(1)> @user see\\u{202E}exe.txt')
  expect(m.text()).toContain('<b>bad</b>')
})

test('a mention opens the ticket at the comment and is marked read', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(10, true)], seen_through: 0, unread: 1 })
  await bell(f.wrapper).trigger('click')
  await list(f.wrapper).get('[data-mention]').trigger('click')
  await flushPromises()
  expect(f.router.currentRoute.value.fullPath).toBe('/4?tab=comments&comment=10')
  expect(f.marks).toEqual([10])
  expect(list(f.wrapper).exists()).toBe(false)
})

test('a question opens the thread at the open question; review and attention open the ticket', async () => {
  const f = await setup([ticket({ number: 13, questions_for_user: 1, column: 'in_progress' }), ticket({ number: 9, column: 'review', review_stage: 'human' })], { mentions: [], seen_through: 0, unread: 0 })
  await bell(f.wrapper).trigger('click')
  await list(f.wrapper).get('[data-waiting="13"]').trigger('click')
  await flushPromises()
  expect(f.threadReads).toEqual([13])
  expect(f.router.currentRoute.value.fullPath).toBe('/13?tab=comments&comment=77')
  await bell(f.wrapper).trigger('click')
  await list(f.wrapper).get('[data-waiting="9"]').trigger('click')
  await flushPromises()
  expect(f.router.currentRoute.value.fullPath).toBe('/9')
})

test('a question that arrives live is announced and opens without reading the thread', async () => {
  const f = await setup([ticket({ number: 13, column: 'in_progress' })], { mentions: [], seen_through: 0, unread: 0 })
  f.hn.omo.emit('comment.created', { comment: comment(88, { ticket: 13, question_to: 'user', mentions: [], body: 'Which colour?' }) }, 13)
  f.hn.omo.change('ticket.updated', ticket({ number: 13, column: 'in_progress', questions_for_user: 1 }))
  await nextTick()
  await nextTick()
  expect(announcements.polite.value).toMatch(/#13/)
  await bell(f.wrapper).trigger('click')
  await list(f.wrapper).get('[data-waiting="13"]').trigger('click')
  await flushPromises()
  expect(f.threadReads).toEqual([])
  expect(f.router.currentRoute.value.fullPath).toBe('/13?tab=comments&comment=88')
})

test('Mark all as read sends the newest id', async () => {
  const f = await setup([], { mentions: [notice(30, true), notice(20, true)], seen_through: 0, unread: 2 })
  await bell(f.wrapper).trigger('click')
  await list(f.wrapper).get('[data-mark-all]').trigger('click')
  await flushPromises()
  expect(f.marks).toEqual([30])
  expect(bell(f.wrapper).attributes('aria-label')).toBe('Notifications: 0 waiting for you, 0 unread mentions')
})

test('the desktop switch shows only where the browser has notifications', async () => {
  const f = await setup([], { mentions: [], seen_through: 0, unread: 0 })
  await bell(f.wrapper).trigger('click')
  expect(list(f.wrapper).find('[data-desktop]').exists()).toBe(typeof globalThis.Notification === 'function')
  mounted?.unmount()
  mounted = null

  vi.stubGlobal(
    'Notification',
    Object.assign(function () {}, { permission: 'default', requestPermission: () => Promise.resolve('denied') }),
  )
  const g = await setup([], { mentions: [], seen_through: 0, unread: 0 })
  await bell(g.wrapper).trigger('click')
  const sw = list(g.wrapper).get('[data-desktop] [role="switch"]')
  expect(sw.attributes('aria-checked')).toBe('false')
  await sw.trigger('click')
  await flushPromises()
  expect(sw.attributes('aria-checked')).toBe('false')
  expect(list(g.wrapper).text()).toMatch(/blocked/)
})

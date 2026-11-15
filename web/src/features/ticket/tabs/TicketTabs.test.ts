import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ticket } from '../../board/testing'
import { project } from '../../../testing/fakeOmo'
import { mountPanel, panelHarness } from '../panelTesting'
import TicketTabs from './TicketTabs.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function open(t: ReturnType<typeof ticket>, path: string) {
  const hn = await panelHarness((omo) => {
    omo.tickets = [t]
    omo.projects = [project('demo')]
  })
  const { wrapper, router } = await mountPanel(hn, TicketTabs, { ticket: t }, path)
  mounted = wrapper
  return { w: wrapper, router }
}
const labels = (w: VueWrapper) => w.findAll('[role="tab"]').map((t) => t.attributes('data-tab'))

test('tabs: Comments, Diff, Agents; Tasks only for a story', async () => {
  const a = await open(ticket({ number: 4 }), '/4')
  expect(labels(a.w)).toEqual(['comments', 'diff', 'agents'])
  a.w.unmount()
  const b = await open(ticket({ number: 5, type: 'story' }), '/5')
  expect(labels(b.w)).toEqual(['comments', 'diff', 'agents', 'tasks'])
})

test('tabs: the query keeps filters and other keys when switching tabs; only the selected panel exists', async () => {
  const { w, router } = await open(ticket({ number: 4 }), '/4?project=demo&needs_me=1&comment=7')
  expect(w.get('[role="tab"][aria-selected="true"]').attributes('data-tab')).toBe('comments')
  expect(w.find('[data-thread]').exists()).toBe(true)
  await w.get('[data-tab="diff"]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ project: 'demo', needs_me: '1', tab: 'diff' })
  expect(w.find('[data-thread]').exists()).toBe(false)
  expect(w.get('[role="tabpanel"]').text()).toContain('Diff')
  await w.get('[data-tab="comments"]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ project: 'demo', needs_me: '1' })
  expect(w.find('[data-thread]').exists()).toBe(true)
})

test('tabs: the address picks the tab; an unknown or inapplicable tab falls back to Comments', async () => {
  const a = await open(ticket({ number: 4 }), '/4?tab=agents')
  expect(a.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('agents')
  a.w.unmount()
  const b = await open(ticket({ number: 4 }), '/4?tab=tasks')
  expect(b.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('comments')
})

import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ticket } from '../../board/testing'
import { project } from '../../../testing/fakeOmo'
import { mountPanel, panelHarness } from '../panelTesting'
import { h } from 'vue'
import TicketTabs from './TicketTabs.vue'
import { pageTabForPanel, panelTabOf } from './ticketTabs'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function open(t: ReturnType<typeof ticket>, path: string, props: Record<string, unknown> = {}, slots?: Record<string, () => unknown>) {
  const hn = await panelHarness((omo) => {
    omo.tickets = [t]
    omo.projects = [project('demo')]
  })
  const { wrapper, router } = await mountPanel(hn, TicketTabs, { ticket: t, ...props }, path, {}, { slots })
  mounted = wrapper
  return { w: wrapper, router }
}
/** A task in project demo. */
const task = (n: number) => ticket({ number: n, projects: [{ project: 'demo', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '', export_error: '', export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '' }] })
const labels = (w: VueWrapper) => w.findAll('[role="tab"]').map((t) => t.attributes('data-tab'))

test('tabs: Comments, Diff, Agents, Forwards; Tasks only for a story', async () => {
  const a = await open(task(4), '/4')
  expect(labels(a.w)).toEqual(['comments', 'diff', 'agents', 'forwards'])
  a.w.unmount()
  const b = await open(ticket({ number: 5, type: 'story' }), '/5')
  expect(labels(b.w)).toEqual(['comments', 'diff', 'agents', 'tasks', 'forwards'])
  b.w.unmount()
  // A task without a project changes no repository: no Diff.
  const c = await open(ticket({ number: 6, projects: [] }), '/6')
  expect(labels(c.w)).toEqual(['comments', 'agents', 'forwards'])
})

test('tabs: the query keeps filters and other keys when switching tabs; only the selected panel exists', async () => {
  const { w, router } = await open(task(4), '/4?project=demo&needs_me=1&comment=7')
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
  const a = await open(task(4), '/4?tab=agents')
  expect(a.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('agents')
  a.w.unmount()
  const b = await open(task(4), '/4?tab=tasks')
  expect(b.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('comments')
})

test('tabs on the page: Overview first and shown by default, from the slot; Comments is then named in the address', async () => {
  const slots = { overview: () => h('p', { 'data-overview': '' }, 'The description') }
  const { w, router } = await open(task(4), '/ticket/4?q=x', { overview: true }, slots)
  expect(labels(w)).toEqual(['overview', 'comments', 'diff', 'agents', 'forwards'])
  expect(w.get('[aria-selected="true"]').attributes('data-tab')).toBe('overview')
  expect(w.find('[data-ticket-tabs="page"]').exists()).toBe(true)
  expect(w.get('[role="tabpanel"] [data-overview]').text()).toBe('The description')
  await w.get('[data-tab="comments"]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ q: 'x', tab: 'comments' })
  expect(w.find('[data-thread]').exists()).toBe(true)
  await w.get('[data-tab="overview"]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({ q: 'x' })
  // The panel has no Overview: an address naming it shows Comments there.
  w.unmount()
  const b = await open(task(4), '/4?tab=overview')
  expect(b.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('comments')
})

test('the tab carries over between the panel and the page', () => {
  // Panel to page: the panel's tab, Comments said explicitly (the page would show Overview).
  expect(panelTabOf(undefined)).toBe('comments')
  expect(panelTabOf('diff')).toBe('diff')
  expect(panelTabOf(['agents'])).toBe('agents')
  expect(panelTabOf('overview')).toBe('comments')
  // Page to panel: nothing for Overview and Comments (the panel's default), else the tab.
  expect(pageTabForPanel(undefined)).toBeUndefined()
  expect(pageTabForPanel('overview')).toBeUndefined()
  expect(pageTabForPanel('comments')).toBeUndefined()
  expect(pageTabForPanel('tasks')).toBe('tasks')
})

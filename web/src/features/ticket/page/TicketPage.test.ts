import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { RouterView, useRoute, useRouter, type RouteRecordRaw } from 'vue-router'
import type { TicketSummary } from '../../../api/types'
import { project } from '../../../testing/fakeOmo'
import { ticket } from '../../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../panelTesting'
import TicketPanel from '../TicketPanel.vue'
import TicketLink from '../TicketLink.vue'
import { PAGE_PREFS_KEY } from './pagePrefs'
import TicketPage from './TicketPage.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.unstubAllGlobals()
  localStorage.removeItem(PAGE_PREFS_KEY)
})

const demo = { project: 'demo', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '', export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '' }

/** The panel at /<n>, as the board mounts it (closing goes to the board). */
const PanelRoute = defineComponent({
  setup() {
    const route = useRoute()
    const router = useRouter()
    return () => h(TicketPanel, { number: Number(route.params.n), onClose: () => void router.push({ name: 'board', query: route.query }) })
  },
})
const routes: RouteRecordRaw[] = [
  { path: '/', name: 'board', component: { render: () => h('p', { 'data-board-here': '' }, 'Board') } },
  { path: '/:n(\\d+)', name: 'ticket', component: PanelRoute },
  { path: '/ticket/:n(\\d+)', name: 'ticket-page', component: TicketPage },
]
/** Shows what the address names, one view per ticket (as LiveBoard keys them). */
const Views = defineComponent({
  setup: () => () => h(RouterView, null, { default: ({ Component, route }: { Component: unknown; route: { path: string } }) => (Component ? h(Component as never, { key: route.path }) : null) }),
})

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  }
}

async function at(hn: PanelHarness, path: string) {
  const { wrapper, router } = await mountPanel(hn, Views, {}, path, {}, { routes })
  mounted = wrapper
  await flushPromises()
  return { w: wrapper, router }
}

/** The page is as wide as `width` (the details column needs 960 px). */
function pageWidth(width: number) {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(private fn: ResizeObserverCallback) {}
      observe() {
        this.fn([{ contentRect: { width } } as ResizeObserverEntry], this as unknown as ResizeObserver)
      }
      disconnect() {}
    },
  )
}

const key = (k: string, target: EventTarget = document.body) => target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))

test('the page: a level-one heading with the focus, the actions, the tabs at the top with Overview first, the description in it', async () => {
  pageWidth(1200)
  const hn = await panelHarness(seeded(ticket({ number: 4, title: 'Login page', projects: [demo] })))
  hn.descriptions.set(4, 'Make it **work**.')
  const { w } = await at(hn, '/ticket/4?q=x')
  const page = w.get('[data-ticket-page]')
  const title = page.get('h1[data-ticket-title]')
  expect(title.text()).toBe('Login page')
  expect(page.attributes('aria-labelledby')).toBe(title.attributes('id'))
  expect(document.activeElement).toBe(title.element)
  expect(page.find('[data-ticket-actions], [data-action]').exists()).toBe(true)
  expect(page.findAll('[role="tab"]').map((t) => t.attributes('data-tab'))).toEqual(['overview', 'comments', 'diff', 'agents', 'forwards'])
  expect(page.get('[role="tab"][aria-selected="true"]').attributes('data-tab')).toBe('overview')
  expect(page.get('[role="tabpanel"] [data-description]').text()).toContain('Make it work.')
  // Wide: the fields stand in the details column, not in Overview.
  expect(page.find('[data-page-fields="side"] [data-ticket-fields]').exists()).toBe(true)
  expect(page.find('[data-page-fields="inline"]').exists()).toBe(false)
  // The way to the panel keeps the board's filters (the way back to the board is the top bar's).
  expect(page.get('[data-open-panel]').attributes('href')).toBe('/4?q=x')
})

test('hiding the details column puts the fields into Overview, remembered for the next page; a narrow page has them there', async () => {
  pageWidth(1200)
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] }), ticket({ number: 5, projects: [demo] })))
  const { w, router } = await at(hn, '/ticket/4')
  await w.get('[data-toggle-details]').trigger('click')
  await flushPromises()
  expect(w.get('[data-toggle-details]').attributes('aria-expanded')).toBe('false')
  expect(w.find('[data-page-fields="side"]').exists()).toBe(false)
  expect(w.find('[role="tabpanel"] [data-page-fields="inline"] [data-ticket-fields]').exists()).toBe(true)
  expect(JSON.parse(localStorage.getItem(PAGE_PREFS_KEY) ?? '{}')).toMatchObject({ details: false })
  await router.push('/ticket/5')
  await flushPromises()
  expect(w.find('[data-page-fields="inline"]').exists()).toBe(true)
  await w.get('[data-toggle-details]').trigger('click')
  await flushPromises()

  vi.unstubAllGlobals()
  pageWidth(600)
  w.unmount()
  const narrow = await at(hn, '/ticket/4')
  expect(narrow.w.find('[data-toggle-details]').exists()).toBe(false)
  expect(narrow.w.find('[data-page-fields="inline"]').exists()).toBe(true)
})

test('a deep link picks the tab; Comments is named; an unknown tab shows Overview', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const a = await at(hn, '/ticket/4?tab=comments')
  expect(a.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('comments')
  expect(a.w.find('[data-thread]').exists()).toBe(true)
  a.w.unmount()
  const b = await at(hn, '/ticket/4?tab=tasks')
  expect(b.w.get('[aria-selected="true"]').attributes('data-tab')).toBe('overview')
})

test('an unknown ticket: says so under a level-one heading, with the way back to the board', async () => {
  const hn = await panelHarness(seeded())
  const { w } = await at(hn, '/ticket/99?story=3')
  expect(w.get('[data-ticket-missing]').text()).toContain('Ticket #99 does not exist.')
  expect(w.get('h1').text()).toBe('Ticket #99')
  const back = w.get('[data-ticket-missing] a')
  expect(back.text()).toBe('Back to the board')
  expect(back.attributes('href')).toBe('/?story=3')
  expect(w.find('[role="tab"]').exists()).toBe(false)
})

test('keys on the page: p opens the panel on the same tab, b goes back to the board; not while typing; Escape stays', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const { w, router } = await at(hn, '/ticket/4?q=x&tab=diff')
  key('Escape')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/ticket/4?q=x&tab=diff')
  // Typed into a field: nothing happens.
  const input = document.createElement('input')
  document.body.append(input)
  key('b', input)
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/ticket/4')
  input.remove()
  key('p')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/4?q=x&tab=diff')
  expect(w.find('dialog[data-ticket-panel]').exists()).toBe(true)

  await router.push('/ticket/4?q=x&tab=comments')
  await flushPromises()
  key('b')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/?q=x')
})

test('the panel: "Open as page" is a link to the page on the shown tab; p goes there; the page has the panel way back', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const { w, router } = await at(hn, '/4?needs_me=1')
  const link = w.get('[data-open-page]')
  expect(link.attributes('href')).toBe('/ticket/4?needs_me=1&tab=comments')
  expect(link.attributes('aria-keyshortcuts')).toBe('p')
  await router.replace('/4?needs_me=1&tab=agents')
  await flushPromises()
  expect(w.get('[data-open-page]').attributes('href')).toBe('/ticket/4?needs_me=1&tab=agents')
  key('p', w.get('dialog[data-ticket-panel]').element)
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/ticket/4?needs_me=1&tab=agents')
  expect(w.find('dialog[data-ticket-panel]').exists()).toBe(false)
  expect(w.get('[data-ticket-page] [aria-selected="true"]').attributes('data-tab')).toBe('agents')
})

test('a description being edited goes along from the panel to the page and back, without a question', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  hn.descriptions.set(4, 'old text')
  const { w, router } = await at(hn, '/4')
  await w.get('[data-edit-description]').trigger('click')
  await w.get('textarea[data-description-input]').setValue('my new text')
  // The page opens on Overview, where its description is.
  expect(w.get('[data-open-page]').attributes('href')).toBe('/ticket/4')
  await w.get('[data-open-page]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/ticket/4')
  expect(w.find('[data-unsaved]').exists()).toBe(false)
  const area = w.get<HTMLTextAreaElement>('[data-ticket-page] textarea[data-description-input]')
  expect(area.element.value).toBe('my new text')
  await router.push('/4')
  await flushPromises()
  expect(w.get<HTMLTextAreaElement>('[data-ticket-panel] textarea[data-description-input]').element.value).toBe('my new text')
  // Leaving the ticket asks; Discard drops the draft for good.
  await router.push('/').catch(() => {})
  await flushPromises()
  expect(w.find('[data-unsaved]').exists()).toBe(true)
  await w.get('[data-discard]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/')
  await router.push('/ticket/4')
  await flushPromises()
  expect(w.find('[data-description-input]').exists()).toBe(false)
})

test('a comment being written goes along between panel and page; a title being edited makes the switch ask', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  hn.write.fn = () => ok({})
  const { w, router } = await at(hn, '/4')
  await w.get('[data-composer] textarea').setValue('half a comment')
  await router.push('/ticket/4?tab=comments')
  await flushPromises()
  expect(w.get<HTMLTextAreaElement>('[data-ticket-page] [data-composer] textarea').element.value).toBe('half a comment')

  await w.get('[data-edit-title]').trigger('click')
  await w.get('[data-title-input]').setValue('A new title')
  await router.push('/4').catch(() => {})
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/ticket/4')
  expect(w.find('[data-unsaved]').exists()).toBe(true)
  // Escape on the page answers the question (keep editing) and leaves nothing.
  key('Escape', w.get('[data-ticket-page]').element)
  await flushPromises()
  expect(w.find('[data-unsaved]').exists()).toBe(false)
  expect(router.currentRoute.value.path).toBe('/ticket/4')
})

test('a ticket link: the address of the page for a new tab; a click stays in the view it is in', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4 }), ticket({ number: 7 })))
  const Linked = defineComponent({ setup: () => () => h('div', [h(TicketLink, { number: 7, tab: 'diff' }, () => '#7'), h(RouterView)]) })
  const { wrapper, router } = await mountPanel(hn, Linked, {}, '/?q=x', {}, { routes })
  mounted = wrapper
  const link = () => wrapper.get('[data-ticket-link]')
  expect(link().attributes('href')).toBe('/ticket/7?q=x&tab=diff')
  // A click with Ctrl (a new tab) is the browser's.
  const ctrl = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })
  link().element.dispatchEvent(ctrl)
  expect(ctrl.defaultPrevented).toBe(false)
  await link().trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/7?q=x&tab=diff')
  await router.push('/ticket/4?q=x')
  await flushPromises()
  await link().trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/ticket/7?q=x&tab=diff')
})

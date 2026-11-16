import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { ticket } from '../board/testing'
import { agent, emitAgent, mountSidebar, sidebarHarness } from './sidebarTesting'
import SidebarRoot from './SidebarRoot.vue'
import { LazyTerminal } from '../terminal/LazyTerminal'
import { resetSidebar, SIDEBAR_KEY, useSidebar } from './useSidebar'

let mounted: VueWrapper | null = null
beforeEach(() => {
  resetSidebar()
  localStorage.clear()
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.unstubAllGlobals()
})

async function open(path = '/') {
  const hn = await sidebarHarness((omo) => {
    omo.tickets = [ticket({ number: 3, title: 'Three', column: 'in_progress' })]
  })
  hn.agents.list = [agent({ id: 'a', ticket: 3 })]
  const { wrapper, router } = await mountSidebar(hn, SidebarRoot, {}, path)
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, router }
}

const tabNames = (w: VueWrapper) => w.findAll('[role="tab"]').map((t) => t.text())

test('collapsed it is a strip of tabs with counts; choosing one expands it on that tab and remembers it', async () => {
  const { w } = await open()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('false')
  expect(tabNames(w)).toEqual(['Agents 1', 'Terminals 0'])
  expect(w.find('[role="tabpanel"]').exists()).toBe(false)
  await w.get('[data-tab="agents"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('true')
  expect(w.get('[role="tab"][aria-selected="true"]').text()).toBe('Agents 1')
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
  expect(JSON.parse(localStorage.getItem(SIDEBAR_KEY)!)).toMatchObject({ open: true, tab: 'agents' })
  await w.get('[data-sidebar-toggle]').trigger('click')
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('false')
  expect(w.get('[data-sidebar-toggle]').attributes('aria-label')).toBe('Expand sidebar')
})

test('the CEO tab shows only while the CEO is enabled', async () => {
  const hn = await sidebarHarness()
  const { wrapper } = await mountSidebar(hn, SidebarRoot)
  mounted = wrapper
  expect(tabNames(wrapper)).not.toContain('CEO')
  hn.omo.emit('settings.changed', { section: 'general', name: '' })
  await flushPromises()
  // The remembered CEO tab falls back to Agents while the tab is hidden.
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, tab: 'ceo', width: 480 }))
  resetSidebar()
  wrapper.unmount()
  const again = await mountSidebar(hn, SidebarRoot)
  mounted = again.wrapper
  expect(again.wrapper.get('[role="tab"][aria-selected="true"]').text()).toMatch(/^Agents/)
})

test('the splitter sets the width in 16 px steps within its limits and the width is remembered', async () => {
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, width: 480, tab: 'agents' }))
  const { w } = await open()
  const sep = w.get('[role="separator"]')
  expect(sep.attributes('aria-valuenow')).toBe('480')
  await sep.trigger('keydown', { key: 'ArrowRight' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('496')
  expect((w.get('.side').element as HTMLElement).style.width).toBe('496px')
  expect(JSON.parse(localStorage.getItem(SIDEBAR_KEY)!).width).toBe(496)
  await sep.trigger('keydown', { key: 'Home' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('320')
  await sep.trigger('keydown', { key: 'ArrowLeft' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('320')
})

test('with storage that fails the page renders and works', async () => {
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  })
  const { w } = await open()
  await w.get('[data-tab="agents"]').trigger('click')
  await flushPromises()
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
})

test('an agent that ends leaves the tab count and the list', async () => {
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, width: 480, tab: 'agents' }))
  const { w, hn } = await open()
  expect(tabNames(w)[0]).toBe('Agents 1')
  emitAgent(hn.omo, 'agent.ended', agent({ id: 'a', ticket: 3, state: 'ended' }))
  await flushPromises()
  expect(tabNames(w)[0]).toBe('Agents 0')
  expect(w.find('[data-agent-row]').exists()).toBe(false)
})

test('the sidebar stays usable while a ticket panel is open: never inert', async () => {
  const { w, router } = await open('/')
  expect(w.get('[data-sidebar]').attributes('inert')).toBeUndefined()
  await router.push('/3')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('inert')).toBeUndefined()
})

test('an open agent terminal stays mounted while a ticket panel is open', async () => {
  const { w, router } = await open('/')
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  await router.push('/3')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  expect(w.find('[data-terminal-covered]').exists()).toBe(false)
})

test('the keyboard opens the sidebar into its tabs and Collapse brings the focus to the toggle', async () => {
  const { w } = await open()
  const strip = w.get('[data-tab="agents"]')
  ;(strip.element as HTMLElement).focus()
  await strip.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[role="tab"][aria-selected="true"]').element)
  await w.get('[data-sidebar-toggle]').trigger('click')
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[data-sidebar-toggle]').element)
})

test('openOn from another feature expands the sidebar and moves the focus into it', async () => {
  const { w } = await open()
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('true')
  expect(document.activeElement).toBe(w.get('[role="tab"][aria-selected="true"]').element)
})

test('on a narrow window the column is gone and the sheet shows the same tabs', async () => {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }))
  const { w } = await open()
  expect(w.find('[data-sidebar]').exists()).toBe(false)
  useSidebar().show()
  await flushPromises()
  const dialog = document.body.querySelector('dialog[open], dialog')!
  expect(dialog.querySelector('h2')!.textContent).toBe('Sidebar')
  expect([...dialog.querySelectorAll('[role="tab"]')].map((t) => t.textContent)).toEqual(['Agents 1', 'Terminals 0'])
  expect(dialog.querySelectorAll('[data-agent-row]')).toHaveLength(1)
})

test('on a narrow window the closed sheet keeps nothing mounted, and opening it puts the focus on the selected tab', async () => {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  const { w } = await open()
  const sheet = () => document.body.querySelector('dialog')!
  expect(sheet().querySelectorAll('[role="tab"]')).toHaveLength(0)
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  expect(document.activeElement?.getAttribute('role')).toBe('tab')
  expect(document.activeElement?.textContent).toBe('Agents 1')
  useSidebar().hide()
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(false)
  expect(sheet().querySelectorAll('[role="tab"]')).toHaveLength(0)
  useSidebar().show()
  await flushPromises()
  expect(document.activeElement?.textContent).toBe('Agents 1')
})

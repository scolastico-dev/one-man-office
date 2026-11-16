import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { TicketSummary, UserTerminal } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { registerTicketAction } from '../ticket/actions'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from '../ticket/panelTesting'
import TicketPanel from '../ticket/TicketPanel.vue'
import { announcements, useAnnouncer } from '../../components/ui/useAnnouncer'
import { createOpenTerminalAction, openTerminalUnavailable } from './openTerminalAction'
import { resetSidebar, useSidebar } from './useSidebar'

let mounted: VueWrapper | null = null
let unregister: (() => void) | null = null
const closePanel = vi.fn()

beforeEach(() => {
  resetSidebar()
  localStorage.clear()
  closePanel.mockReset()
  unregister = registerTicketAction(createOpenTerminalAction({ closePanel }))
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
  unregister?.()
  document.body.innerHTML = ''
})

const wt = (id: string, dir = `/wt/${id}/12`) => ({ project: id, base_branch: 'main', branch: 'omo/12', worktree: dir, export_url: '', export_state: '' as const, export_error: '' })

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo', 'Demo project'), project('other', 'Other project')]
  }
}

async function open(hn: PanelHarness, n: number) {
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: n }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return wrapper
}

const shell = (over: Partial<UserTerminal> = {}): UserTerminal => ({
  id: 'sh1',
  title: 'demo',
  dir: '/wt/demo/12',
  project: 'demo',
  ticket: 12,
  state: 'running',
  exit_error: '',
  started_at: '2026-11-15T10:00:00Z',
  ended_at: null,
  ...over,
})

test('Open terminal is unavailable before work starts and on Done, with the reason', async () => {
  expect(openTerminalUnavailable(ticket({ number: 12, column: 'ready', projects: [{ ...wt('demo'), worktree: '' }] }))).toBe('No worktree yet: work on #12 has not started.')
  expect(openTerminalUnavailable(ticket({ number: 12, column: 'ready', projects: [] }))).toBe('No worktree yet: work on #12 has not started.')
  expect(openTerminalUnavailable(ticket({ number: 12, column: 'done', projects: [wt('demo')] }))).toBe('Work on #12 is finished; open a terminal in the project instead.')
  expect(openTerminalUnavailable(ticket({ number: 12, column: 'in_progress', projects: [wt('demo')] }))).toBeUndefined()

  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'ready', projects: [{ ...wt('demo'), worktree: '' }] })))
  const w = await open(hn, 12)
  const button = w.get('[data-action="open-terminal"]')
  expect(button.attributes('disabled')).toBeDefined()
  expect(w.text()).toContain('No worktree yet: work on #12 has not started.')
  await button.trigger('click')
  expect(hn.sent).toEqual([])
})

test('one worktree: the shell opens, the panel closes and the sidebar shows it on the Terminals tab', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo')] })))
  hn.write.fn = () => ok(shell())
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/terminals', body: { project: 'demo', ticket: 12 } }])
  expect(closePanel).toHaveBeenCalledOnce()
  const s = useSidebar()
  expect([s.open.value, s.tab.value, s.selection.value]).toEqual([true, 'terminals', 'sh1'])
})

test('when the panel refuses to close (unsaved changes), the shell is open but the sidebar does not open over the panel', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo')] })))
  hn.write.fn = () => ok(shell())
  closePanel.mockResolvedValue(false)
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/terminals', body: { project: 'demo', ticket: 12 } }])
  expect(closePanel).toHaveBeenCalledOnce()
  const s = useSidebar()
  expect([s.open.value, s.selection.value, s.focusRequests.value]).toEqual([false, null, 0])
})

test('a refusal stays in the panel in the server words and the panel stays open', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo')] })))
  for (const [status, code, message] of [
    [409, 'conflict', '16 terminals are open; close one first.'],
    [422, 'project_unavailable', 'The folder of Demo project is missing.'],
    [503, 'unavailable', 'omo is stopping.'],
  ] as const) {
    hn.write.fn = () => refuse(code, message, status)
    const w = await open(hn, 12)
    await w.get('[data-action="open-terminal"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain(message)
    w.unmount()
    mounted = null
  }
  expect(closePanel).not.toHaveBeenCalled()
  expect(useSidebar().open.value).toBe(false)
})

test('several worktrees ask which project, then open the chosen one', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo'), wt('other')] })))
  hn.write.fn = () => ok(shell({ id: 'sh2', project: 'other' }))
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([])
  const choices = [...document.body.querySelectorAll<HTMLButtonElement>('[data-choose-project]')]
  expect(choices.map((b) => b.textContent)).toEqual(['Demo project', 'Other project'])
  choices[1]!.click()
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/terminals', body: { project: 'other', ticket: 12 } }])
  expect(document.body.querySelector('[data-choose-project]')).toBeNull()
  expect(useSidebar().selection.value).toBe('sh2')
})

test('closing the project dialog sends nothing and announces nothing', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo'), wt('other')] })))
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  useAnnouncer().polite('before closing')
  await flushPromises()
  const chooser = document.body.querySelector('[data-choose-project]')!.closest('dialog')!
  chooser.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click()
  await flushPromises()
  expect(document.body.querySelector('[data-choose-project]')).toBeNull()
  expect(hn.sent).toEqual([])
  expect(announcements.polite.value).toBe('before closing')
  // The action can be used again.
  expect(w.get('[data-action="open-terminal"]').attributes('disabled')).toBeUndefined()
})

test('the project dialog goes away with the panel and nothing is sent', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo'), wt('other')] })))
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  expect(document.body.querySelector('[data-choose-project]')).not.toBeNull()
  w.unmount()
  mounted = null
  await flushPromises()
  expect(document.body.querySelector('[data-choose-project]')).toBeNull()
  expect(hn.sent).toEqual([])
})

test('a refusal while choosing stays in the project dialog', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 12, column: 'in_progress', projects: [wt('demo'), wt('other')] })))
  hn.write.fn = () => refuse('conflict', 'the worktree recorded for #12 is not one omo made; open a terminal in the project instead', 409)
  const w = await open(hn, 12)
  await w.get('[data-action="open-terminal"]').trigger('click')
  await flushPromises()
  document.body.querySelector<HTMLButtonElement>('[data-choose-project]')!.click()
  await flushPromises()
  expect(document.body.querySelector('[data-action-error]')!.textContent).toContain('is not one omo made')
  expect(closePanel).not.toHaveBeenCalled()
})

import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { ApiError } from '../../api/client'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'

const exported = {
  project: 'demo', base_branch: 'main', branch: 'omo/2', worktree: '', export_url: '', export_state: '' as const,
  export_error: '', export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '',
  last_export_commit: '1111111111111111111111111111111111111111', last_export_url: 'https://example.invalid/pull/1',
  review_commit: '2222222222222222222222222222222222222222',
}
let mounted: VueWrapper | null = null
let harness: PanelHarness | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  harness?.session.store.stop()
  harness = null
})
async function open(overrides: Partial<TicketSummary> = {}) {
  harness = await panelHarness((omo) => {
    omo.tickets = [ticket({ number: 2, column: 'review', review_stage: 'awaiting_merge', projects: [exported], ...overrides })]
    omo.projects = [project('demo')]
  })
  harness.write.fn = () => ok({})
  mounted = (await mountPanel(harness, TicketPanel, { number: 2 }, '/2')).wrapper
  return { w: mounted, hn: harness }
}

test.each(['developer', 'reviewer'] as const)('follow-up sends a task to %s only after a nonempty task is confirmed', async (role) => {
  const { w, hn } = await open()
  await w.get('[data-action="follow-up"]').trigger('click')
  const dialog = w.get('[data-follow-up-dialog]')
  expect(hn.sent).toEqual([])
  await dialog.get('select').setValue(role)
  await dialog.get('textarea').setValue('   ')
  await dialog.get('form').trigger('submit')
  expect(hn.sent).toEqual([])
  expect(dialog.text()).toContain('Write the follow-up task.')
  await dialog.get('textarea').setValue('  Address the PR feedback.  ')
  await dialog.get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/2/follow-up', body: { role, comment: 'Address the PR feedback.' } }])
  expect(w.find('[data-follow-up-dialog]').exists()).toBe(false)
})

test('a story offers its product manager and reviewer; product manager uses developer on the wire', async () => {
  const { w, hn } = await open({ type: 'story', review_stage: 'human' })
  await w.get('[data-action="follow-up"]').trigger('click')
  expect(w.get('select option[value="developer"]').text()).toBe('Product manager')
  expect(w.get('select option[value="reviewer"]').text()).toBe('Reviewer')
  await w.get('[data-follow-up-dialog] textarea').setValue('Coordinate the requested changes.')
  await w.get('[data-follow-up-dialog] form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ role: 'developer', comment: 'Coordinate the requested changes.' })
})

test('a refused follow-up keeps the selected recipient and task for correction', async () => {
  const { w, hn } = await open()
  hn.write.fn = () => refuse('invalid_transition', 'The export is still running.', 409)
  await w.get('[data-action="follow-up"]').trigger('click')
  await w.get('[data-follow-up-dialog] select').setValue('reviewer')
  await w.get('[data-follow-up-dialog] textarea').setValue('Check the updated implementation.')
  await w.get('[data-follow-up-dialog] form').trigger('submit')
  await flushPromises()
  expect(w.get('[data-follow-up-dialog] [role="alert"]').text()).toContain('The export is still running.')
  expect((w.get('[data-follow-up-dialog] select').element as HTMLSelectElement).value).toBe('reviewer')
  expect((w.get('[data-follow-up-dialog] textarea').element as HTMLTextAreaElement).value).toBe('Check the updated implementation.')
})

test('closing the ticket closes the follow-up dialog without submitting it', async () => {
  const { w, hn } = await open()
  await w.get('[data-action="follow-up"]').trigger('click')
  w.unmount()
  mounted = null
  await flushPromises()
  expect(document.querySelector('[data-follow-up-dialog]')).toBeNull()
  expect(hn.sent).toEqual([])
})

test('a lost follow-up answer closes the dialog without offering an unsafe duplicate submission', async () => {
  const { w, hn } = await open()
  hn.write.fn = () => { throw new ApiError(0, 'network', 'No answer') }
  await w.get('[data-action="follow-up"]').trigger('click')
  await w.get('[data-follow-up-dialog] textarea').setValue('Check the feedback.')
  await w.get('[data-follow-up-dialog] form').trigger('submit')
  await flushPromises()
  expect(w.find('[data-follow-up-dialog]').exists()).toBe(false)
  expect(hn.sent).toHaveLength(1)
})

test('the previous pull request stays accessible during follow-up without hiding a failed re-export', async () => {
  const { w } = await open({ review_stage: 'human', projects: [{ ...exported, export_state: 'failed', export_error: 'Remote refused the update.' }] })
  expect(w.get('[data-last-export-url]').attributes('href')).toBe('https://example.invalid/pull/1')
  expect(w.get('[data-branch="demo"] [data-export-state="failed"]').text()).toContain('Remote refused the update.')
})

test('an unsafe previous pull request URL cannot become a link', async () => {
  const { w } = await open({ projects: [{ ...exported, last_export_url: 'javascript:alert(1)' }] })
  expect(w.find('[data-last-export-url]').exists()).toBe(false)
})

test.each([
  { column: 'review', review_stage: 'agent' },
  { column: 'review', review_stage: 'exporting' },
  { column: 'in_progress', review_stage: '' },
  { column: 'done', review_stage: '' },
  { column: 'review', review_stage: 'human', projects: [{ ...exported, last_export_commit: '' }] },
] as Partial<TicketSummary>[])('follow-up is absent outside exported human/awaiting merge reviews: %j', async (state) => {
  const { w } = await open(state)
  expect(w.find('[data-action="follow-up"]').exists()).toBe(false)
})

test('follow-up is disabled while omo is working', async () => {
  const { w } = await open({ busy: true })
  expect(w.get('[data-action="follow-up"]').attributes('disabled')).toBeDefined()
})

test.each(['export', 'approve'] as const)('%s re-exports reviewed follow-up work and warns about the earlier exported commit', async (action) => {
  const { w, hn } = await open({ review_stage: 'human', effective_when_approved: 'export' })
  expect(w.get('[data-action="export"]').text()).toBe('Re-export…')
  expect(w.get('[data-action="approve"]').text()).toBe('Approve and re-export…')
  await w.get(`[data-action="${action}"]`).trigger('click')
  const dialog = w.get('[data-approve-dialog]')
  const warning = dialog.get('[data-reexport-warning]')
  expect(warning.text()).toContain('demo')
  expect(warning.text()).toContain('111111111111')
  expect(warning.text()).toContain('222222222222')
  expect(dialog.get('[data-approve-effect]').text()).toContain('omo pushes the reviewed changes')
  const confirm = w.findAll('button').find((b) => b.text() === (action === 'export' ? 'Re-export' : 'Approve and re-export'))!
  await confirm.trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([action === 'export'
    ? { method: 'POST', path: '/api/tickets/2/approve', body: { export: true } }
    : { method: 'POST', path: '/api/tickets/2/move', body: { column: 'done', before: null } }])
})

test.each(['', exported.last_export_commit])('no commit warning when the review commit is absent or already exported: %s', async (review_commit) => {
  const { w } = await open({ review_stage: 'human', projects: [{ ...exported, review_commit }] })
  await w.get('[data-action="export"]').trigger('click')
  expect(w.find('[data-reexport-warning]').exists()).toBe(false)
})

import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { exportAction } from './exportAction'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'
import type { ActionContext } from './actions'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const demo = { project: 'demo', base_branch: 'main', branch: 'omo/2', worktree: '', export_url: '', export_state: '' as const, export_error: '' }

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  }
}

async function open(hn: PanelHarness, n: number) {
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: n }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return wrapper
}

const ctx = { targets: { done: { allowed: true } } } as unknown as ActionContext

test('Export shows for a story or standalone task at the human stage only, whatever when_approved says', () => {
  const human: Partial<TicketSummary> = { column: 'review', review_stage: 'human', projects: [demo] }
  for (const when of ['merge', 'export'] as const) {
    expect(exportAction.applies(ticket({ ...human, effective_when_approved: when }), ctx)).toBe(true)
    expect(exportAction.applies(ticket({ ...human, type: 'story', effective_when_approved: when }), ctx)).toBe(true)
  }
  expect(exportAction.applies(ticket({ ...human, story: 7 }), ctx)).toBe(false)
  expect(exportAction.applies(ticket({ ...human, review_stage: 'agent' }), ctx)).toBe(false)
  expect(exportAction.applies(ticket({ ...human, review_stage: 'merging' }), ctx)).toBe(false)
  expect(exportAction.applies(ticket({ ...human, column: 'in_progress' }), ctx)).toBe(false)
  expect(exportAction.applies(ticket({ ...human, column: 'done' }), ctx)).toBe(false)
})

test('Export sends {export:true}; Merge locally instead sends {merge:true}', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 2, column: 'review', review_stage: 'human', projects: [demo] }),
      ticket({ number: 5, column: 'review', review_stage: 'human', effective_when_approved: 'export', projects: [demo] }),
    ),
  )
  hn.write.fn = () => ok({})
  const w = await open(hn, 2)
  await w.get('[data-action="export"]').trigger('click')
  const dlg = w.get('[data-approve-dialog]')
  expect(dlg.get('[data-approve-effect]').text()).toContain('omo pushes the branch and opens a pull request')
  expect(dlg.text()).toContain('The ticket is done once the pull request exists.')
  expect(w.find('[data-merge-instead]').exists()).toBe(false)
  await w.findAll('button').find((b) => b.text() === 'Export' && b.attributes('data-action') === undefined)!.trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/2/approve', body: { export: true } }])
  expect(w.find('[data-approve-dialog]').exists()).toBe(false)
  w.unmount()
  mounted = null

  // With the setting on export, Approve exports and offers a local merge.
  const w2 = await open(hn, 5)
  await w2.get('[data-action="approve"]').trigger('click')
  expect(w2.get('[data-approve-dialog] [data-approve-effect]').text()).toContain('opens a pull request')
  await w2.get('[data-merge-instead]').trigger('click')
  await flushPromises()
  expect(hn.sent[1]).toEqual({ method: 'POST', path: '/api/tickets/5/approve', body: { merge: true } })
})

test('a refused export stays in the dialog with the server\'s words', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 2, column: 'review', review_stage: 'human', projects: [demo] })))
  hn.write.fn = () => refuse('invalid_transition', '#2 is already approved and merging', 409)
  const w = await open(hn, 2)
  await w.get('[data-action="export"]').trigger('click')
  await w.findAll('button').find((b) => b.text() === 'Export' && b.attributes('data-action') === undefined)!.trigger('click')
  await flushPromises()
  expect(w.get('[data-approve-dialog] [data-action-error]').text()).toBe('#2 is already approved and merging')
})

test('after an export that failed in part, the dialog says which projects are exported again and what a local merge merges', async () => {
  const other = { ...demo, project: 'other', branch: 'omo/5-other', export_state: 'failed' as const, export_error: 'no remote to push to' }
  const hn = await panelHarness(
    seeded(
      ticket({
        number: 5,
        type: 'story',
        column: 'review',
        review_stage: 'human',
        effective_when_approved: 'export',
        attention: 'Export of other failed: no remote to push to',
        projects: [{ ...demo, export_url: 'https://example.invalid/pull/1', export_state: 'exported' as const }, other],
      }),
    ),
  )
  const w = await open(hn, 5)
  expect(w.get('[data-branch="other"] [data-export-state]').text()).toBe('export failed: no remote to push to')
  expect(w.get('[data-branch="demo"] [data-export-state="pull_request"]').attributes('href')).toBe('https://example.invalid/pull/1')
  await w.get('[data-action="approve"]').trigger('click')
  const dlg = w.get('[data-approve-dialog]')
  expect(dlg.get('[data-approve-effect]').text()).toMatch(/^omo exports only the projects without a pull request \(other\); \S+ keeps its\./)
  expect(dlg.get('[data-merge-instead-note]').text()).toMatch(/^Merge locally instead merges only the projects without a pull request \(other\); \S+ keeps its pull request\.$/)
})

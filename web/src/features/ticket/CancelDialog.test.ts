import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import CancelDialog from './CancelDialog.vue'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse } from './panelTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const demo = { project: 'demo', base_branch: 'main', branch: 'omo/1', worktree: '/w/1', export_url: '' }

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  }
}

test('cancel never sends discard_uncommitted and explains what happens to a dirty worktree', async () => {
  const t = ticket({
    number: 1,
    column: 'in_progress',
    projects: [demo],
    agents: [{ session: 's', role: 'developer', name: 'dev', state: 'running' }],
  })
  const hn = await panelHarness(seeded(t))
  const { wrapper: w } = await mountPanel(hn, TicketPanel, { number: 1 }, '/1')
  mounted = w
  await w.get('[data-action="cancel"]').trigger('click')
  const dlg = w.get('[data-cancel-dialog]')
  expect(dlg.text()).toContain('The agent stops and the ticket is closed as cancelled.')
  // Keeping the branch is the default.
  expect((dlg.get('input[value="keep"]').element as HTMLInputElement).checked).toBe(true)
  expect(dlg.get('[data-cancel-effect]').text()).toContain('The branch and its worktree stay')

  await dlg.get('input[value="delete"]').setValue(true)
  expect(dlg.get('[data-cancel-effect]').text()).toContain(
    'If the worktree has uncommitted changes, omo keeps the folder and the branch and lists the files in a comment on the ticket.',
  )

  // A refusal is shown in the dialog, which keeps the choice.
  hn.write.fn = () => refuse('conflict', 'omo is starting work on or merging #1; try again in a moment.', 409)
  await w.get('#cancel-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'POST', path: '/api/tickets/1/cancel', body: { delete_branch: true } })
  expect(w.get('[data-cancel-dialog] [data-action-error]').text()).toBe('omo is starting work on or merging #1; try again in a moment.')
  expect((w.get('input[value="delete"]').element as HTMLInputElement).checked).toBe(true)

  hn.write.fn = () => ok({ ...t, column: 'done', resolution: 'cancelled', description: '' })
  await w.get('#cancel-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[1]).toEqual({ method: 'POST', path: '/api/tickets/1/cancel', body: { delete_branch: true } })
  for (const s of hn.sent) expect(JSON.stringify(s.body)).not.toContain('discard')
  expect(w.find('[data-cancel-dialog]').exists()).toBe(false)
})

test('cancel dialog for a story lists its open tasks, and its merged tasks when deleting the branch', async () => {
  const story = ticket({ number: 10, type: 'story', title: 'Story', column: 'in_progress', projects: [demo] })
  const tasks = [
    ticket({ number: 11, story: 10, title: 'Open task', column: 'in_progress' }),
    ticket({ number: 12, story: 10, title: 'Merged task', column: 'done', resolution: 'completed' }),
    ticket({ number: 13, story: 10, title: 'Cancelled task', column: 'done', resolution: 'cancelled' }),
    ticket({ number: 14, story: 10, title: 'Ready task', column: 'ready' }),
  ]
  const hn = await panelHarness(seeded(story, ...tasks, ticket({ number: 20, title: 'Unrelated' })))
  const { wrapper: w } = await mountPanel(hn, CancelDialog, { open: true, ticket: story }, '/10')
  mounted = w
  await flushPromises()
  expect(w.get('[data-cancel-tasks]').findAll('li').map((li) => li.text())).toEqual([
    '#11 Open task (In Progress)',
    '#14 Ready task (Ready For Dev)',
  ])
  expect(w.find('[data-cancel-merged]').exists()).toBe(false)
  await w.get('input[value="delete"]').setValue(true)
  expect(w.get('[data-cancel-merged]').findAll('li').map((li) => li.text())).toEqual(['#12 Merged task'])
  await w.get('#cancel-form').trigger('submit')
  expect(w.findComponent(CancelDialog).emitted('confirm')).toEqual([[true]])
})

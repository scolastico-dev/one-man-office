import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError } from '../../api/client'
import type { TicketProject, TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse } from './panelTesting'
import { canRemoveProject, workOf } from './removeProject'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const proj = (id: string, branch = 'omo/1-s'): TicketProject => ({
  project: id,
  base_branch: 'main',
  branch,
  worktree: branch === '' ? '' : `/home/you/.local/omo/worktrees/${id}/1`,
  export_url: '',
  export_state: '',
  export_error: '',
})

test('a project with a branch can be removed while the ticket waits for the user', () => {
  const two = [proj('api'), proj('ui')]
  const base = { type: 'story' as const, projects: two }
  expect(canRemoveProject(ticket({ ...base, column: 'planning' }), two[1]!)).toBe(true)
  expect(canRemoveProject(ticket({ ...base, column: 'ready' }), two[1]!)).toBe(true)
  expect(canRemoveProject(ticket({ ...base, column: 'review', review_stage: 'human' }), two[1]!)).toBe(true)
  for (const t of [
    ticket({ ...base, column: 'in_progress' }),
    ticket({ ...base, column: 'review', review_stage: 'agent' }),
    ticket({ ...base, column: 'review', review_stage: 'exporting' }),
    ticket({ ...base, column: 'done', resolution: 'completed' }),
    ticket({ ...base, column: 'ready', busy: true }),
  ]) {
    expect(canRemoveProject(t, two[1]!)).toBe(false)
  }
  // The last project stays; one without a branch is changed in the projects field.
  expect(canRemoveProject(ticket({ column: 'ready', projects: [proj('api')] }), proj('api'))).toBe(false)
  expect(canRemoveProject(ticket({ ...base, column: 'ready', projects: [proj('api'), proj('ui', '')] }), proj('ui', ''))).toBe(false)
})

test('workOf reads the work a refused removal lists, and nothing else', () => {
  const details = { project: 'ui', branch: 'omo/1-s', base: 'main', commits: ['abc123 fix'], files: [] }
  expect(workOf(new ApiError(409, 'conflict', 'would delete work', details))).toEqual(details)
  expect(workOf(new ApiError(409, 'conflict', 'reviewer works there'))).toBeNull()
  expect(workOf(new ApiError(422, 'validation', 'x', details))).toBeNull()
  expect(workOf(new ApiError(409, 'conflict', 'x', { ...details, commits: [], files: [] }))).toBeNull()
  expect(workOf(new Error('x'))).toBeNull()
})

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('api'), project('ui')]
  }
}

test('Remove from ticket names what goes, asks again for work that would be lost, then sends discard', async () => {
  const t = ticket({
    number: 1,
    type: 'story',
    column: 'review',
    review_stage: 'human',
    projects: [proj('api'), proj('ui')],
    agents: [{ session: 's', role: 'reviewer', name: 'reviewer-bo', state: 'waiting', purpose: 'work', project: 'ui' }],
  })
  const hn = await panelHarness(seeded(t))
  const { wrapper: w } = await mountPanel(hn, TicketPanel, { number: 1 }, '/1')
  mounted = w
  await w.get('[data-remove-project="ui"]').trigger('click')
  const dlg = w.get('[data-remove-project-dialog]')
  expect(dlg.get('[data-remove-effects]').text()).toContain('/home/you/.local/omo/worktrees/ui/1')
  expect(dlg.get('[data-remove-effects]').text()).toContain('omo/1-s')
  expect(dlg.get('[data-remove-agents]').text()).toContain('reviewer-bo')

  hn.write.fn = () =>
    refuse('conflict', 'removing project "ui" from #1 would delete work', 409, {
      project: 'ui',
      branch: 'omo/1-s',
      base: 'main',
      commits: ['abc1234 change the ui'],
      files: ['notes.txt'],
    })
  await w.get('#remove-project-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'POST', path: '/api/tickets/1/remove-project', body: { project: 'ui' } })
  expect(w.get('[data-remove-commits]').text()).toContain('abc1234 change the ui')
  expect(w.get('[data-remove-files]').text()).toContain('notes.txt')
  expect(w.find('[data-remove-project-dialog] [data-action-error]').exists()).toBe(false)

  hn.write.fn = () => ok({ ...t, projects: [proj('api')], description: '' })
  await w.get('#remove-project-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[1]).toEqual({ method: 'POST', path: '/api/tickets/1/remove-project', body: { project: 'ui', discard: true } })
  expect(w.find('[data-remove-project-dialog]').exists()).toBe(false)
})

test('a refusal without work to list is shown in the dialog', async () => {
  const t = ticket({ number: 1, type: 'story', column: 'ready', projects: [proj('api'), proj('ui')] })
  const hn = await panelHarness(seeded(t))
  const { wrapper: w } = await mountPanel(hn, TicketPanel, { number: 1 }, '/1')
  mounted = w
  await w.get('[data-remove-project="ui"]').trigger('click')
  hn.write.fn = () => refuse('validation', 'task #3 of story #1 uses project "ui" and is not done; cancel or finish it first', 422)
  await w.get('#remove-project-form').trigger('submit')
  await flushPromises()
  expect(w.get('[data-remove-project-dialog] [data-action-error]').text()).toContain('task #3')
  expect(hn.sent).toHaveLength(1)
})

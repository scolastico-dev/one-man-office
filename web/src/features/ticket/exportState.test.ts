import { expect, test } from 'vitest'
import type { ExportState, TicketProject } from '../../api/types'
import { ticket } from '../board/testing'
import { exportEffect, mergeInsteadNote, projectExport, projectExportWords } from './exportState'

const proj = (project: string, export_url = '', export_state: ExportState = export_url ? 'exported' : '', export_error = ''): TicketProject => ({
  project,
  base_branch: 'main',
  branch: `omo/1-${project}`,
  worktree: '',
  export_url,
  export_state,
  export_error,
  export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '',
})
const names: Record<string, string> = { api: 'API', web: 'Web', docs: 'Docs' }
const nameOf = (id: string) => names[id] ?? id

test('some exported, one failed, one left out: each project says where it stands, with no parsing of the attention', () => {
  const t = ticket({
    type: 'story',
    column: 'review',
    review_stage: 'human',
    projects: [proj('api', 'https://example.invalid/pull/1'), proj('web', '', 'failed', 'no remote to push to; try again'), proj('docs', '', 'left_out')],
    attention: 'something else entirely',
  })
  expect(projectExport(t, 'api')).toEqual({ kind: 'pull_request', url: 'https://example.invalid/pull/1' })
  expect(projectExportWords(projectExport(t, 'web')!)).toBe('export failed: no remote to push to; try again')
  expect(projectExportWords(projectExport(t, 'docs')!)).toBe('left out: nothing to export')
})

test('a project left out and one not exported yet read differently', () => {
  const t = ticket({
    type: 'story',
    column: 'review',
    review_stage: 'human',
    projects: [proj('api', 'https://example.invalid/pull/1'), proj('web'), proj('docs', '', 'left_out')],
  })
  expect(projectExportWords(projectExport(t, 'web')!)).toBe('no pull request')
  expect(projectExportWords(projectExport(t, 'docs')!)).toBe('left out: nothing to export')
})

test('exporting says so; a ticket that is not exporting, or was merged, says nothing', () => {
  const going = ticket({ column: 'review', review_stage: 'exporting', projects: [proj('api', '', 'exporting')] })
  expect(projectExportWords(projectExport(going, 'api')!)).toBe('being exported…')
  expect(projectExport(ticket({ column: 'done', resolution: 'completed', projects: [proj('api')] }), 'api')).toBeNull()
  expect(projectExport(ticket({ column: 'review', review_stage: 'human', projects: [proj('api')] }), 'api')).toBeNull()
  expect(projectExport(going, 'unknown')).toBeNull()
})

test('the export effect is per project, and after a partial export names what is left and what keeps its pull request', () => {
  const one = ticket({ projects: [proj('api')] })
  expect(exportEffect(one, nameOf)).toBe(
    'omo pushes the branch and opens a pull request; a branch without commits of its own has nothing to export. The ticket waits in Review until the exported work is merged into the remote target branch.',
  )
  const three = ticket({ type: 'story', projects: [proj('api'), proj('web'), proj('docs')] })
  expect(exportEffect(three, nameOf)).toContain('leaves out a project with nothing to export')
  expect(exportEffect(three, nameOf)).toContain('A project that cannot be exported does not stop the others; the ticket then stays in Review naming it.')
  expect(mergeInsteadNote(three, nameOf)).toBe('')

  const partial = ticket({ type: 'story', projects: [proj('api', 'https://example.invalid/pull/1'), proj('web'), proj('docs')] })
  expect(exportEffect(partial, nameOf)).toBe(
    'omo exports only the projects without a pull request (Web and Docs); API keeps its. A project with nothing to export is left out. The ticket waits in Review until every exported project is merged into its remote target branch.',
  )
  expect(mergeInsteadNote(partial, nameOf)).toBe('Merge locally instead merges only the projects without a pull request (Web and Docs); API keeps its pull request.')
})


test('the approval effect explains that exporting waits for a remote merge', () => {
  for (const projects of [[proj('api')], [proj('api'), proj('web')], [proj('api', 'https://example.invalid/pull/1'), proj('web')]]) {
    const effect = exportEffect(ticket({ projects }), nameOf)
    expect(effect).toContain('waits in Review')
    expect(effect).toContain('merge')
    expect(effect).not.toContain('done once')
  }
})

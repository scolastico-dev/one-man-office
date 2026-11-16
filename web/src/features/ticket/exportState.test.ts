import { expect, test } from 'vitest'
import type { TicketProject } from '../../api/types'
import { ticket } from '../board/testing'
import { exportEffect, failedExports, mergeInsteadNote, projectExport, projectExportWords } from './exportState'

const proj = (project: string, export_url = ''): TicketProject => ({ project, base_branch: 'main', branch: `omo/1-${project}`, worktree: '', export_url })
const names: Record<string, string> = { api: 'API', web: 'Web', docs: 'Docs' }
const nameOf = (id: string) => names[id] ?? id

test('the failed projects and their reasons are read from the attention', () => {
  const f = failedExports('Export of api failed: git push: no remote; but it tried; Export of docs failed: the job crashed three times')
  expect([...f]).toEqual([
    ['api', 'git push: no remote; but it tried'],
    ['docs', 'the job crashed three times'],
  ])
  expect(failedExports('').size).toBe(0)
})

test('some exported, some failed, one left out: each project says where it stands', () => {
  const t = ticket({
    type: 'story',
    column: 'review',
    review_stage: 'human',
    projects: [proj('api', 'https://example.invalid/pull/1'), proj('web'), proj('docs')],
    attention: 'Export of web failed: no remote to push to',
  })
  expect(projectExport(t, 'api')).toEqual({ kind: 'pull_request', url: 'https://example.invalid/pull/1' })
  expect(projectExportWords(projectExport(t, 'web')!)).toBe('export failed: no remote to push to')
  expect(projectExportWords(projectExport(t, 'docs')!)).toBe('no pull request')
})

test('done and exported: a project without a pull request was left out; exporting says so; a merged ticket says nothing', () => {
  const done = ticket({ column: 'done', resolution: 'exported', projects: [proj('api', 'https://example.invalid/pull/1'), proj('docs')] })
  expect(projectExportWords(projectExport(done, 'docs')!)).toBe('left out: nothing to export')
  const going = ticket({ column: 'review', review_stage: 'exporting', projects: [proj('api')] })
  expect(projectExportWords(projectExport(going, 'api')!)).toBe('being exported…')
  expect(projectExport(ticket({ column: 'done', resolution: 'completed', projects: [proj('api')] }), 'api')).toBeNull()
  expect(projectExport(ticket({ column: 'review', review_stage: 'human', projects: [proj('api')] }), 'api')).toBeNull()
})

test('the export effect is per project, and after a partial export names what is left and what keeps its pull request', () => {
  const one = ticket({ projects: [proj('api')] })
  expect(exportEffect(one, nameOf)).toBe(
    'omo pushes the branch and opens a pull request; a branch without commits of its own has nothing to export. The ticket is done once the pull request exists.',
  )
  const three = ticket({ type: 'story', projects: [proj('api'), proj('web'), proj('docs')] })
  expect(exportEffect(three, nameOf)).toContain('leaves out a project with nothing to export')
  expect(exportEffect(three, nameOf)).toContain('A project that cannot be exported does not stop the others; the ticket then stays in Review naming it.')
  expect(mergeInsteadNote(three, nameOf)).toBe('')

  const partial = ticket({ type: 'story', projects: [proj('api', 'https://example.invalid/pull/1'), proj('web'), proj('docs')] })
  expect(exportEffect(partial, nameOf)).toBe(
    'omo exports only the projects without a pull request (Web and Docs); API keeps its. A project with nothing to export is left out. The ticket is done once every other project has its pull request.',
  )
  expect(mergeInsteadNote(partial, nameOf)).toBe('Merge locally instead merges only the projects without a pull request (Web and Docs); API keeps its pull request.')
})

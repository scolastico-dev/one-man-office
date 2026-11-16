import { expect, test } from 'vitest'
import { ticket } from '../board/testing'
import {
  addDependencyState,
  BUSY_REASON,
  dependencyCandidates,
  DONE_REASON,
  fieldState,
  matchCandidates,
  supervisorOptions,
  unsatisfiedWords,
  withoutProject,
} from './fieldRules'

const ctx = { ceoEnabled: false }
const branched = [{ project: 'demo', base_branch: 'main', branch: 'omo/7', worktree: '', export_url: '', export_state: '' as const, export_error: '' }]

test('fieldState locks projects, story and base branches while a branch exists', () => {
  const t = ticket({ number: 7, story: 3, column: 'planning', projects: branched })
  for (const f of ['projects', 'story'] as const) {
    expect(fieldState(t, f, ctx)).toEqual({ visible: true, editable: false, lockedReason: 'Locked while the branch omo/7 exists.' })
  }
  const standalone = ticket({ number: 8, column: 'planning', projects: branched })
  expect(fieldState(standalone, 'base_branch', ctx).lockedReason).toBe('Locked while the branch omo/7 exists.')
  // The other fields stay open, also after the ticket went back to Planning.
  expect(fieldState(standalone, 'model', ctx).editable).toBe(true)
  expect(fieldState(standalone, 'supervisor', ctx).editable).toBe(true)
  expect(fieldState(ticket({ projects: [{ ...branched[0]!, branch: '' }] }), 'projects', ctx).editable).toBe(true)
})

test('nothing is editable on a done ticket and a busy one locks placement only', () => {
  const done = ticket({ column: 'done', resolution: 'completed', projects: [{ ...branched[0]!, branch: '' }] })
  for (const f of ['projects', 'story', 'depends_on', 'supervisor', 'model', 'review', 'when_approved']) {
    const s = fieldState(done, f as never, ctx)
    expect(s.editable, f).toBe(false)
    expect(s.lockedReason, f).toBe(DONE_REASON)
  }
  expect(fieldState(done, 'dates', ctx)).toEqual({ visible: true, editable: false })
  const busy = ticket({ column: 'ready', busy: true })
  expect(fieldState(busy, 'projects', ctx).lockedReason).toBe(BUSY_REASON)
  expect(fieldState(busy, 'story', ctx).lockedReason).toBe(BUSY_REASON)
  expect(fieldState(busy, 'supervisor', ctx).editable).toBe(true)
  expect(fieldState(busy, 'depends_on', ctx).editable).toBe(true)
  expect(addDependencyState(busy)).toEqual({ allowed: false, reason: BUSY_REASON })
  expect(addDependencyState(ticket())).toEqual({ allowed: true })
})

test('a story whose task is busy can still get a dependency; only its own claim stops it', () => {
  const story = ticket({ number: 3, type: 'story', column: 'in_progress', busy: true })
  const task = ticket({ number: 4, story: 3, busy: true })
  expect(addDependencyState(story, () => [task])).toEqual({ allowed: true })
  // Busy with no busy task: the story's own claim, which the server refuses.
  expect(addDependencyState(story, () => [ticket({ number: 4, story: 3 })])).toEqual({ allowed: false, reason: BUSY_REASON })
  expect(addDependencyState(story)).toEqual({ allowed: false, reason: BUSY_REASON })
  // A task's own busy flag always refuses.
  expect(addDependencyState(task, () => [task])).toEqual({ allowed: false, reason: BUSY_REASON })
})

test('fieldState hides when-approved for a task in a story and pm supervisor elsewhere', () => {
  const inStory = ticket({ story: 3 })
  expect(fieldState(inStory, 'when_approved', ctx).visible).toBe(false)
  expect(fieldState(inStory, 'base_branch', ctx).visible).toBe(false)
  expect(fieldState(ticket({ projects: branched }), 'when_approved', ctx).visible).toBe(true)
  expect(supervisorOptions(inStory, ctx)).toEqual(['user', 'pm'])
  expect(supervisorOptions(ticket(), ctx)).toEqual(['user'])
  expect(supervisorOptions(ticket(), { ceoEnabled: true })).toEqual(['user', 'ceo'])
  // A CEO that was switched off meanwhile stays visible as the current value.
  expect(supervisorOptions(ticket({ supervisor: 'ceo' }), ctx)).toEqual(['user', 'ceo'])
})

test('release, parallel limit and story are shown for the type they belong to', () => {
  const story = ticket({ type: 'story' })
  expect(fieldState(story, 'task_release', ctx).visible).toBe(true)
  expect(fieldState(story, 'max_parallel', ctx).visible).toBe(true)
  expect(fieldState(story, 'story', ctx).visible).toBe(false)
  expect(fieldState(ticket(), 'task_release', ctx).visible).toBe(false)
  expect(fieldState(ticket(), 'max_parallel', ctx).visible).toBe(false)
  expect(fieldState(ticket(), 'story', ctx).visible).toBe(true)
})

const world = [
  ticket({ number: 1, type: 'story', title: 'Story one' }),
  ticket({ number: 2, type: 'story', title: 'Story two' }),
  ticket({ number: 3, story: 1, title: 'Task in one' }),
  ticket({ number: 4, story: 1, title: 'Another in one' }),
  ticket({ number: 5, story: 2, title: 'Task in two' }),
  ticket({ number: 6, title: 'Standalone six' }),
  ticket({ number: 7, title: 'Standalone seven' }),
  ticket({ number: 8, title: 'Gone', column: 'done', resolution: 'cancelled' }),
]
const numbers = (l: { number: number }[]) => l.map((x) => x.number)

test('dependency candidates follow the scope rules', () => {
  expect(numbers(dependencyCandidates(world[2]!, world))).toEqual([4])
  expect(numbers(dependencyCandidates(world[0]!, world))).toEqual([2])
  expect(numbers(dependencyCandidates(world[5]!, world))).toEqual([1, 2, 7])
  // Existing links, a direct cycle and cancelled tickets are not offered.
  const t = { ...world[5]!, depends_on: [1], blocks: [7] }
  expect(numbers(dependencyCandidates(t, world))).toEqual([2])
})

test('candidates are found by number or title, the exact number first', () => {
  const list = dependencyCandidates(world[5]!, [...world, ticket({ number: 70, title: 'Seventy things' })])
  expect(numbers(matchCandidates(list, '#7'))).toEqual([7, 70])
  expect(numbers(matchCandidates(list, 'story t'))).toEqual([2])
  expect(numbers(matchCandidates(list, ''))).toEqual(numbers(list))
})

test('an unsatisfied dependency says why in the board\'s words', () => {
  const t = ticket({
    depends_on: [12, 13, 14, 15],
    waiting_for: [
      { ticket: 12, reason: 'not_done' },
      { ticket: 13, reason: 'export_not_merged' },
      { ticket: 14, reason: 'cancelled' },
    ],
  })
  expect(unsatisfiedWords(t, 12)).toBe('waiting for #12')
  expect(unsatisfiedWords(t, 13)).toBe('pull request not merged')
  expect(unsatisfiedWords(t, 14)).toBe('cancelled')
  expect(unsatisfiedWords(t, 15)).toBe('')
})

test('a task without a project has no "when approved" and no base branch, and its placement locks once its working folder exists', () => {
  const none = ticket({ number: 9, column: 'ready', projects: [] })
  expect(withoutProject(none)).toBe(true)
  expect(withoutProject(ticket({ type: 'story', projects: [] }))).toBe(false)
  expect(withoutProject(ticket({ projects: branched }))).toBe(false)
  expect(fieldState(none, 'when_approved', ctx).visible).toBe(false)
  expect(fieldState(none, 'base_branch', ctx).visible).toBe(false)
  expect(fieldState(none, 'projects', ctx).editable).toBe(true)
  const started = ticket({ number: 9, column: 'ready', projects: [], workdir: '/home/you/.local/omo/workdirs/9' })
  for (const f of ['projects', 'story'] as const) {
    expect(fieldState(started, f, ctx)).toEqual({ visible: true, editable: false, lockedReason: 'Locked: work started without a project, in its working folder.' })
  }
  expect(fieldState(started, 'model', ctx).editable).toBe(true)
})

import { expect, test } from 'vitest'
import type { PromptInfo } from '../../../api/types'
import { groupPrompts } from './promptGroups'

const NAMES = [
  'ceo', 'pm', 'developer', 'reviewer', 'export',
  'ceo-instructions', 'pm-instructions', 'developer-instructions', 'reviewer-instructions', 'export-instructions',
  'start', 'restart', 'handoff', 'wait-timeout', 'wait-investigate', 'stalled', 'question-reminder',
]

function infoOf(name: string): PromptInfo {
  const kind = name.endsWith('-instructions') ? 'instructions' : ['ceo', 'pm', 'developer', 'reviewer', 'export'].includes(name) ? 'role' : 'message'
  return { name, kind, customized: false, revision: '0' }
}

test('groups: every name in the list lands in one group; instructions first', () => {
  const groups = groupPrompts(NAMES.map(infoOf))
  expect(groups.map((g) => g.id)).toEqual(['ceo', 'pm', 'developer', 'reviewer', 'export', 'messages'])
  const landed = groups.flatMap((g) => g.prompts.map((p) => p.name))
  expect([...landed].sort()).toEqual([...NAMES].sort())
  expect(new Set(landed).size).toBe(NAMES.length)
  for (const g of groups.slice(0, 5)) expect(g.prompts.map((p) => p.kind)).toEqual(['instructions', 'role'])
  expect(groups[5]!.prompts.map((p) => p.name)).toEqual(['start', 'restart', 'handoff', 'wait-timeout', 'wait-investigate', 'stalled', 'question-reminder'])
})

test('groups: titles and one-line explanations per name', () => {
  const groups = groupPrompts(NAMES.map(infoOf))
  const stalled = groups[5]!.prompts.find((p) => p.name === 'stalled')!
  expect(stalled.title).toBe('Stalled')
  expect(stalled.description).toBe('Typed into an agent that has shown no output for the silent time.')
  const dev = groups[2]!
  expect(dev.title).toBe('Developer')
  expect(dev.prompts[0]!.title).toBe('Your instructions')
  expect(dev.prompts[0]!.description).toBe("Inserted into the developer's prompt. Empty, the prompt has no such part.")
  expect(dev.prompts[1]!.action).toBe("Show omo's prompt for this role")
})

test('groups: a name the page does not know is still shown, and what the list says is kept', () => {
  const list: PromptInfo[] = [
    { name: 'qa-instructions', kind: 'instructions', customized: true, revision: 'ab12' },
    { name: 'qa', kind: 'role', customized: false, revision: 'cd34' },
    { name: 'novel', kind: 'message', customized: true, revision: 'ef56' },
    { name: 'developer', kind: 'role', customized: false, revision: '0' },
  ]
  const groups = groupPrompts(list)
  expect(groups.map((g) => g.id)).toEqual(['developer', 'qa', 'messages'])
  expect(groups[1]!.prompts.map((p) => [p.name, p.customized, p.revision])).toEqual([
    ['qa-instructions', true, 'ab12'],
    ['qa', false, 'cd34'],
  ])
  expect(groups[2]!.prompts[0]!).toMatchObject({ name: 'novel', title: 'novel', customized: true })
  expect(groupPrompts([])).toEqual([])
})

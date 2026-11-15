import { expect, test } from 'vitest'
import type { SkillSet } from '../../../api/types'
import {
  SKILL_ROLES,
  SUPERPOWERS_PRESET,
  blankSkillSet,
  errorsOfSkillSet,
  fileOfSkillSet,
  freeName,
  hasSuperpowers,
  superpowersPreset,
  toDraft,
  toSkillSet,
  updateResult,
  updateWords,
} from './skillForms'

const stored = (o: Partial<SkillSet> = {}): SkillSet => ({ name: 'sp', repository: 'https://example.invalid/x.git', ref: '', update: 'at_start', enabled: true, instructions: {}, ...o })

test('the Superpowers preset is a new unsaved set, not a stored one', () => {
  const p = superpowersPreset()
  expect(p.repository).toBe('https://github.com/obra/superpowers.git')
  expect(p.ref).toBe('main')
  expect(p.update).toBe('at_start')
  expect(p.instructions.developer).toContain('test-driven-development')
  // a copy: editing it never changes the constant
  p.instructions.developer = 'changed'
  expect(SUPERPOWERS_PRESET.instructions.developer).toContain('test-driven-development')
  expect(superpowersPreset('superpowers-2').name).toBe('superpowers-2')
  expect(Object.isFrozen(SUPERPOWERS_PRESET)).toBe(true)
})

test('role texts map to instructions: only roles with a text, every text as typed', () => {
  const d = blankSkillSet('a')
  d.instructions.developer = '  Read the skills.\n\n'
  d.instructions.export = 'x'
  expect(toSkillSet(d).instructions).toEqual({ developer: '  Read the skills.\n\n', export: 'x' })
  expect(toDraft(stored({ instructions: { pm: 'p' } })).instructions).toEqual({ ceo: '', pm: 'p', developer: '', reviewer: '', export: '' })
  expect(SKILL_ROLES.map((r) => r.id)).toEqual(['ceo', 'pm', 'developer', 'reviewer', 'export'])
  // reading and writing again changes nothing
  const s = stored({ instructions: { reviewer: 'r' } })
  expect(toSkillSet(toDraft(s))).toEqual(s)
})

test('the preset is offered until a set has its address, however it is spelled', () => {
  expect(hasSuperpowers([])).toBe(false)
  expect(hasSuperpowers([stored()])).toBe(false)
  expect(hasSuperpowers([stored({ repository: 'https://github.com/obra/superpowers' })])).toBe(true)
  expect(hasSuperpowers([stored({ repository: 'HTTPS://GitHub.com/obra/superpowers.git/' })])).toBe(true)
})

test('a free name is found for a taken one', () => {
  expect(freeName('superpowers', [])).toBe('superpowers')
  expect(freeName('superpowers', ['superpowers', 'superpowers-2'])).toBe('superpowers-3')
})

test('the update mode and results are said in words', () => {
  expect(updateWords('at_start', 'main')).toBe('updates at every start from main')
  expect(updateWords('at_start', '')).toBe('updates at every start')
  expect(updateWords('pinned', '')).toBe('pinned to the default branch')
  const base = { name: 'a', path: '', updated_at: null }
  expect(updateResult({ ...base, state: 'ok', commit: 'a41c9e0123', message: '' })).toEqual({ ok: true, text: 'Updated; now at a41c9e0.' })
  expect(updateResult({ ...base, state: 'failed', commit: '', message: 'boom' })).toEqual({ ok: false, text: 'The update failed: boom' })
})

test('a settings file belongs to the set it is named after', () => {
  expect(fileOfSkillSet('/home/x/omo/skillsets/sp.yaml')).toBe('sp')
  expect(fileOfSkillSet('C:\\omo\\skillsets\\sp.yml')).toBe('sp')
  expect(fileOfSkillSet('/home/x/omo/roles/sp.yaml')).toBeNull()
  expect(errorsOfSkillSet([{ file: '/o/skillsets/a.yaml', message: 'm' }, { file: '/o/skillsets/b.yaml', message: 'n' }], 'b')).toEqual([{ file: '/o/skillsets/b.yaml', message: 'n' }])
})

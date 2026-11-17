import { expect, test } from 'vitest'
import type { SkillSearchResolved, SkillSearchResult } from '../../../api/types'
import { toSkillSet } from './skillForms'
import { draftFromSearch, folderNote, installWords, originOf, searchText, setNameOf } from './skillSearch'

const result: SkillSearchResult = {
  id: 'mattpocock/skills/grill-me',
  name: 'grill-me',
  skill_id: 'grill-me',
  source: 'mattpocock/skills',
  installs: 1565244,
  repository: 'https://github.com/mattpocock/skills.git',
  url: 'https://skills.sh/mattpocock/skills/grill-me',
}
const resolved: SkillSearchResolved = {
  repository: 'https://github.com/mattpocock/skills.git',
  ref: '',
  paths: ['skills/productivity/grill-me'],
  found: true,
  commit: 'abc',
}

test('a chosen result becomes an unsaved set with its repository, the default branch, its folder and no role', () => {
  const d = draftFromSearch(result, resolved, [])
  expect(toSkillSet(d)).toEqual({
    name: 'grill-me',
    repository: 'https://github.com/mattpocock/skills.git',
    ref: '',
    update: 'at_start',
    enabled: true,
    paths: ['skills/productivity/grill-me'],
    roles: [],
    instructions: {},
  })
  // The draft shares no list with the answer.
  d.paths.push('x')
  expect(resolved.paths).toEqual(['skills/productivity/grill-me'])
})

test('a taken name gets a number', () => {
  expect(draftFromSearch(result, resolved, ['grill-me']).name).toBe('grill-me-2')
})

test('a skill not found gives no path', () => {
  const d = draftFromSearch(result, { ...resolved, paths: [], found: false }, [])
  expect(d.paths).toEqual([])
  expect(folderNote(originOf(result, { ...resolved, paths: [], found: false }))).toMatch(/did not find a folder/)
  expect(folderNote(originOf(result, { ...resolved, paths: [], found: true }))).toBe('The whole repository is this skill.')
  expect(folderNote(originOf(result, resolved))).toContain('skills/productivity/grill-me')
})

test('set names follow the settings name rule', () => {
  const rule = /^[a-z0-9][a-z0-9._-]{0,63}$/
  for (const [id, want] of [
    ['grill-me', 'grill-me'],
    ['React_Native', 'react_native'],
    ['-.x', 'x'],
    ['__', 'skill'],
    ['a'.repeat(200), 'a'.repeat(64)],
  ] as const) {
    expect(setNameOf(id)).toBe(want)
    expect(setNameOf(id)).toMatch(rule)
  }
  expect(draftFromSearch({ ...result, skill_id: 'b'.repeat(100) }, resolved, []).name).toMatch(rule)
})

test('install counts in words', () => {
  expect(installWords(1565244)).toBe('1.6M installs')
  expect(installWords(4794)).toBe('4.8K installs')
  expect(installWords(2000)).toBe('2K installs')
  expect(installWords(1)).toBe('1 install')
  expect(installWords(12)).toBe('12 installs')
  expect(installWords(0)).toBe('no installs')
})

test('the search text is trimmed and needs two characters', () => {
  expect(searchText(' g ')).toBeNull()
  expect(searchText('  grill   me ')).toBe('grill me')
  expect(searchText('x'.repeat(150))).toHaveLength(100)
})

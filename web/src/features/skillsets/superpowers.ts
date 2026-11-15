// The Superpowers skill set (https://github.com/obra/superpowers): what the
// Skill sets page's "Add Superpowers" fills in and what the first steps add
// in one click. Outside the settings, so the board can use it without
// loading the settings' code.
import type { SkillSet } from '../../api/types'

/** The repository of the Superpowers skill set. */
export const SUPERPOWERS_ADDRESS = 'https://github.com/obra/superpowers.git'

/** The set as stored: updated at every start from main, one instruction per role that needs one. */
export const SUPERPOWERS_SKILL_SET: Readonly<SkillSet> = Object.freeze({
  name: 'superpowers',
  repository: SUPERPOWERS_ADDRESS,
  ref: 'main',
  update: 'at_start',
  enabled: true,
  instructions: Object.freeze({
    pm: 'Before you split a story, read skills/brainstorming/SKILL.md and follow it.',
    developer: 'Before you write code, read skills/test-driven-development/SKILL.md and follow it.',
    reviewer: 'Review with skills/requesting-code-review/SKILL.md.',
  }) as Record<string, string>,
})

const sameAddress = (a: string, b: string) => {
  const clean = (s: string) => s.trim().replace(/\/+$/, '').replace(/\.git$/, '').toLowerCase()
  return clean(a) === clean(b)
}

/** Whether a set already has the Superpowers repository. */
export function hasSuperpowers(sets: readonly Pick<SkillSet, 'repository'>[]): boolean {
  return sets.some((s) => sameAddress(s.repository, SUPERPOWERS_ADDRESS))
}

/** `base`, or `base-2`, `base-3`… when the name is taken. */
export function freeName(base: string, taken: readonly string[]): string {
  if (!taken.includes(base)) return base
  for (let n = 2; ; n++) if (!taken.includes(`${base}-${n}`)) return `${base}-${n}`
}

/** The Superpowers set to create under a name no set has yet. */
export function superpowersSkillSet(taken: readonly string[]): SkillSet {
  return { ...SUPERPOWERS_SKILL_SET, name: freeName(SUPERPOWERS_SKILL_SET.name, taken), instructions: { ...SUPERPOWERS_SKILL_SET.instructions } }
}

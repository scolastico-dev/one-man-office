// The grill-me skill (https://github.com/mattpocock/skills): a recommended
// skill set for the CEO only. grill-me itself only hands over to the
// grilling skill next to it, so the set takes both folders.
import type { SkillSet } from '../../api/types'
import { sameAddress } from './superpowers'

/** The repository that holds grill-me. */
export const GRILL_ME_ADDRESS = 'https://github.com/mattpocock/skills.git'

/** The folder of the grill-me skill in that repository. */
export const GRILL_ME_PATH = 'skills/productivity/grill-me'

/** The set as stored: the two skill folders, for the CEO, updated at every start from main. */
export const GRILL_ME_SKILL_SET: Readonly<SkillSet> = Object.freeze({
  name: 'grill-me',
  repository: GRILL_ME_ADDRESS,
  ref: 'main',
  update: 'at_start',
  enabled: true,
  paths: [GRILL_ME_PATH, 'skills/productivity/grilling'],
  roles: ['ceo'],
  instructions: Object.freeze({
    ceo: 'When the user asks you to grill them on a plan or an idea, follow the grill-me skill.',
  }) as Record<string, string>,
})

/** Whether a set already gives grill-me: its repository with the whole repository or the grill-me folder. */
export function hasGrillMe(sets: readonly Pick<SkillSet, 'repository' | 'paths'>[]): boolean {
  return sets.some((s) => sameAddress(s.repository, GRILL_ME_ADDRESS) && (s.paths.length === 0 || s.paths.includes(GRILL_ME_PATH)))
}

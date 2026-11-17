// Searching skills.sh from the Skills page, as plain functions. omo's
// server asks skills.sh (the page never does) and answers checked results;
// every text of a result is still someone else's and is shown as plain text.
// A chosen result becomes a new, unsaved skill set: its repository, the
// default branch, the skill's folder when omo found it, a name made from the
// skill's id, every role left for the user to choose.
import type { SkillSearchResolved, SkillSearchResult } from '../../../api/types'
import { freeName } from '../../skillsets/superpowers'
import { blankSkillSet, type SkillDraft } from './skillForms'

/** The fewest characters a search sends (the server refuses fewer). */
export const MIN_SEARCH = 2
/** The most characters a search sends. */
export const MAX_SEARCH = 100
/** How long typing must pause before the page searches, in milliseconds. */
export const SEARCH_DELAY = 300
/** skills.sh, for the note at the search. */
export const SKILLS_SH = 'https://skills.sh'

/** Where a new skill set came from: a search result, for the warning in its form. */
export interface SkillOrigin {
  name: string
  source: string
  url: string
  /** The skill's folder omo found in the repository ("": the whole repository is the skill); null: not found. */
  folder: string | null
}

/** The text a search sends: trimmed; null while it is too short. */
export function searchText(typed: string): string | null {
  const t = typed.trim().replace(/\s+/g, ' ')
  return [...t].length < MIN_SEARCH ? null : [...t].slice(0, MAX_SEARCH).join('')
}

/** An install count in words: "1.6M installs", "4.8K installs", "1 install". */
export function installWords(n: number): string {
  const short = (v: number) => v.toFixed(1).replace(/\.0$/, '')
  if (!Number.isFinite(n) || n <= 0) return 'no installs'
  if (n >= 1_000_000) return `${short(n / 1_000_000)}M installs`
  if (n >= 1_000) return `${short(n / 1_000)}K installs`
  return `${n} install${n === 1 ? '' : 's'}`
}

/** A skill set name made from a skill's id: lower case, other characters made dashes, at most 64. */
export function setNameOf(skillId: string): string {
  const name = skillId
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[^a-z0-9]+/, '')
    .slice(0, 64)
  return name === '' ? 'skill' : name
}

/** The new, unsaved skill set for a chosen result, under a name no set has yet. */
export function draftFromSearch(result: SkillSearchResult, resolved: SkillSearchResolved, taken: readonly string[]): SkillDraft {
  // The name leaves room for freeName's "-2".
  const draft = blankSkillSet(freeName(setNameOf(result.skill_id).slice(0, 60), taken))
  draft.repository = resolved.repository
  draft.ref = resolved.ref
  draft.paths = [...resolved.paths]
  return draft
}

/** Where the set came from, for its form. */
export function originOf(result: SkillSearchResult, resolved: SkillSearchResolved): SkillOrigin {
  return { name: result.name, source: result.source, url: result.url, folder: resolved.found ? (resolved.paths[0] ?? '') : null }
}

/** The warning a set from a search shows, and asks again on its first save. The same kind of warning as for a plugin nobody reviewed. */
export const SEARCH_TRUST_WARNING: Readonly<{ title: string; paragraphs: readonly string[] }> = Object.freeze({
  title: 'Nobody reviewed this skill',
  paragraphs: Object.freeze([
    'A skill is instructions your agents follow. They run as you, with your rights: what a skill tells them, they can do to your files and accounts.',
    'skills.sh lists skills anyone publishes, and omo’s maintainers have not reviewed this one. Save it only when you know and trust where it comes from, and read its files first.',
  ]),
})

/** What the form says about the skill's folder. */
export function folderNote(o: SkillOrigin): string {
  if (o.folder === null) return 'omo did not find a folder for this skill in the repository. Add its folder under Folders and files, or the set gives the whole repository.'
  if (o.folder === '') return 'The whole repository is this skill.'
  return `omo found the skill in the folder ${o.folder} and filled it in.`
}

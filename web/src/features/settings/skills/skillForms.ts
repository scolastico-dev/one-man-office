// What the skill set forms show and send, as plain functions. A skill set's
// texts are instructions every agent of a role is given, so the form keeps
// them exactly as typed; the browser only leaves out a role with no text.
import type { FileError, SkillSet, SkillSetState, SkillSetUpdate } from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'
import { SUPERPOWERS_SKILL_SET } from '../../skillsets/superpowers'
import { shortCommit } from '../extensionWords'

export type SkillRole = 'ceo' | 'pm' | 'developer' | 'reviewer' | 'export'

/** The roles a skill set can tell something, in the order the form shows them. */
export const SKILL_ROLES: readonly { id: SkillRole; label: string }[] = [
  { id: 'ceo', label: 'CEO' },
  { id: 'pm', label: 'Product manager' },
  { id: 'developer', label: 'Developer' },
  { id: 'reviewer', label: 'Reviewer' },
  { id: 'export', label: 'Export' },
]

/** A skill set as the form edits it: one text per role, empty when the role is told nothing. */
export interface SkillDraft {
  name: string
  repository: string
  ref: string
  update: SkillSetUpdate
  enabled: boolean
  instructions: Record<SkillRole, string>
}

const emptyTexts = (): Record<SkillRole, string> => ({ ceo: '', pm: '', developer: '', reviewer: '', export: '' })

/** The stored set as a draft; roles the file does not name get an empty text. */
export function toDraft(s: SkillSet): SkillDraft {
  const instructions = emptyTexts()
  for (const r of SKILL_ROLES) instructions[r.id] = s.instructions?.[r.id] ?? ''
  return { name: s.name, repository: s.repository, ref: s.ref, update: s.update, enabled: s.enabled, instructions }
}

/** The draft as the server stores it: only roles with a text, every text as typed. */
export function toSkillSet(d: SkillDraft): SkillSet {
  const instructions: Record<string, string> = {}
  for (const r of SKILL_ROLES) if (d.instructions[r.id] !== '') instructions[r.id] = d.instructions[r.id]
  return { name: d.name, repository: d.repository, ref: d.ref, update: d.update, enabled: d.enabled, instructions }
}

/** Where a set not stored yet starts: nothing filled in. */
export function blankSkillSet(name: string): SkillDraft {
  return { name, repository: '', ref: '', update: 'at_start', enabled: true, instructions: emptyTexts() }
}

export { freeName, hasSuperpowers, SUPERPOWERS_ADDRESS } from '../../skillsets/superpowers'

/** "Add Superpowers": a filled-in form that is stored only when the user saves it. */
export const SUPERPOWERS_PRESET: Readonly<SkillDraft> = Object.freeze(toDraft(SUPERPOWERS_SKILL_SET))

/** The preset as a new, unsaved set under `name`. */
export function superpowersPreset(name: string = SUPERPOWERS_PRESET.name): SkillDraft {
  return { ...SUPERPOWERS_PRESET, name, instructions: { ...SUPERPOWERS_PRESET.instructions } }
}

export const UPDATE_OPTIONS: SelectOption[] = [
  { value: 'at_start', label: 'Update at every start' },
  { value: 'pinned', label: 'Keep this version (pinned)' },
]

/** The update mode in words, for the list. */
export function updateWords(update: SkillSetUpdate, ref: string): string {
  if (update === 'pinned') return `pinned to ${ref || 'the default branch'}`
  return `updates at every start${ref ? ` from ${ref}` : ''}`
}

/** The state of a set before the server told one. */
export const missingState = (name: string): SkillSetState => ({ name, state: 'missing', commit: '', path: '', updated_at: null, message: '' })

/** What "Update now" answered, in words (the failure text is the server's, to be shown through `visibleText`). */
export function updateResult(s: SkillSetState): { ok: boolean; text: string } {
  switch (s.state) {
    case 'ok':
      return { ok: true, text: s.commit === '' ? 'Updated.' : `Updated; now at ${shortCommit(s.commit)}.` }
    case 'failed':
      return { ok: false, text: `The update failed: ${s.message}` }
    case 'updating':
      return { ok: true, text: 'An update is already running.' }
    default:
      return { ok: false, text: 'Nothing was checked out.' }
  }
}

/** The name of the set a settings file belongs to (`…/skillsets/<name>.yaml`), or null. */
export function fileOfSkillSet(file: string): string | null {
  const m = /[\\/]skillsets[\\/]([^\\/]+)\.ya?ml$/.exec(file)
  return m === null ? null : (m[1] ?? null)
}

/** The broken files that belong to the set `name`. */
export function errorsOfSkillSet(errors: readonly FileError[], name: string): FileError[] {
  return errors.filter((e) => fileOfSkillSet(e.file) === name)
}

/** The refusals (by field) that no field of the form shows. */
export const PLACED_FIELDS: readonly string[] = ['repository', 'ref', 'update', ...SKILL_ROLES.map((r) => `instructions.${r.id}`)]

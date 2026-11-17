// What the skill set forms show and send, as plain functions. A skill set
// reaches the roles it names (none named: every role), with the skills its
// paths select (none: the whole repository) and a text per role. The texts
// are instructions agents are given, so the form keeps them exactly as
// typed; the browser leaves out a role with no text and the text of a role
// that does not get the set.
import type { FileError, SkillSet, SkillSetState, SkillSetUpdate } from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'
import { hasSuperpowers, SUPERPOWERS_SKILL_SET } from '../../skillsets/superpowers'
import { shortCommit } from '../extensionWords'
import { freeName } from '../../skillsets/superpowers'

export type SkillRole = 'ceo' | 'pm' | 'developer' | 'reviewer' | 'export'

/** The roles a skill set can tell something, in the order the form shows them. */
export const SKILL_ROLES: readonly { id: SkillRole; label: string }[] = [
  { id: 'ceo', label: 'CEO' },
  { id: 'pm', label: 'Product manager' },
  { id: 'developer', label: 'Developer' },
  { id: 'reviewer', label: 'Reviewer' },
  { id: 'export', label: 'Export' },
]

/** A skill set as the form edits it: its paths and roles as lists, one text per role (empty: no text). */
export interface SkillDraft {
  name: string
  repository: string
  ref: string
  update: SkillSetUpdate
  enabled: boolean
  /** Relative to the repository's root; empty: the whole repository. */
  paths: string[]
  /** In the order of SKILL_ROLES; empty: every role. */
  roles: SkillRole[]
  instructions: Record<SkillRole, string>
}

const emptyTexts = (): Record<SkillRole, string> => ({ ceo: '', pm: '', developer: '', reviewer: '', export: '' })

const isRole = (r: string): r is SkillRole => SKILL_ROLES.some((x) => x.id === r)

/** The stored set as a draft; roles the file does not name get an empty text; roles in the form's order. */
export function toDraft(s: SkillSet): SkillDraft {
  const instructions = emptyTexts()
  for (const r of SKILL_ROLES) instructions[r.id] = s.instructions?.[r.id] ?? ''
  const roles = SKILL_ROLES.map((r) => r.id).filter((r) => (s.roles ?? []).includes(r))
  // A role omo does not know is kept, so the server names it in its refusal.
  for (const r of s.roles ?? []) if (!isRole(r)) roles.push(r as SkillRole)
  return { name: s.name, repository: s.repository, ref: s.ref, update: s.update, enabled: s.enabled, paths: [...(s.paths ?? [])], roles, instructions }
}

/** The roles whose agents get the set: the chosen ones, every role when none is chosen. */
export function rolesGetting(d: Pick<SkillDraft, 'roles'>): typeof SKILL_ROLES {
  return d.roles.length === 0 ? SKILL_ROLES : SKILL_ROLES.filter((r) => d.roles.includes(r.id))
}

/** The draft as the server stores it: paths as typed, only texts of roles that get the set and have one. */
export function toSkillSet(d: SkillDraft): SkillSet {
  const instructions: Record<string, string> = {}
  for (const r of rolesGetting(d)) if (d.instructions[r.id] !== '') instructions[r.id] = d.instructions[r.id]
  return { name: d.name, repository: d.repository, ref: d.ref, update: d.update, enabled: d.enabled, paths: [...d.paths], roles: [...d.roles], instructions }
}

/** The draft with `role` chosen or no longer chosen; the roles stay in the form's order. */
export function toggleRole(d: SkillDraft, role: SkillRole): SkillRole[] {
  const on = d.roles.includes(role)
  return SKILL_ROLES.map((r) => r.id).filter((r) => (r === role ? !on : d.roles.includes(r)))
}

/** Who gets the set, in words. */
export function rolesWords(roles: readonly string[]): string {
  if (roles.length === 0) return 'every role'
  return roles.map((r) => SKILL_ROLES.find((x) => x.id === r)?.label ?? r).join(', ')
}

/** Who gets a stored set and how much of it, in words: "for CEO · 2 paths", "for every role · the whole repository". */
export function reachWords(s: Pick<SkillSet, 'roles' | 'paths'>): string {
  const what = s.paths.length === 0 ? 'the whole repository' : s.paths.length === 1 ? (s.paths[0] ?? '') : `${s.paths.length} paths`
  return `for ${rolesWords(s.roles)} · ${what}`
}

/** Where a set not stored yet starts: nothing filled in (every role, the whole repository). */
export function blankSkillSet(name: string): SkillDraft {
  return { name, repository: '', ref: '', update: 'at_start', enabled: true, paths: [], roles: [], instructions: emptyTexts() }
}

export { freeName, hasSuperpowers, SUPERPOWERS_ADDRESS } from '../../skillsets/superpowers'

/** A copy of a draft that shares no list or text map with it. */
const copy = (d: SkillDraft, name: string): SkillDraft => ({ ...d, name, paths: [...d.paths], roles: [...d.roles], instructions: { ...d.instructions } })

/** "Add Superpowers": a filled-in form that is stored only when the user saves it. */
export const SUPERPOWERS_PRESET: Readonly<SkillDraft> = Object.freeze(toDraft(SUPERPOWERS_SKILL_SET))

/** The preset as a new, unsaved set under `name`. */
export function superpowersPreset(name: string = SUPERPOWERS_PRESET.name): SkillDraft {
  return copy(SUPERPOWERS_PRESET, name)
}

/** A recommended skill set the page offers to add while no set gives it. */
export interface Recommendation {
  id: string
  /** The button: "Add <label>". */
  label: string
  preset: Readonly<SkillDraft>
  present(sets: readonly SkillSet[]): boolean
}

/** The recommended skill sets, in the order of their buttons. */
export const RECOMMENDATIONS: readonly Recommendation[] = [
  { id: 'superpowers', label: 'Superpowers', preset: SUPERPOWERS_PRESET, present: hasSuperpowers },
]

/** A recommendation as a new, unsaved set under a name no set has yet. */
export function presetOf(rec: Recommendation, taken: readonly string[]): SkillDraft {
  return copy(rec.preset, freeName(rec.preset.name, taken))
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
export const missingState = (name: string): SkillSetState => ({ name, state: 'missing', commit: '', path: '', updated_at: null, message: '', missing_paths: [] })

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
export function isPlacedField(field: string): boolean {
  return /^(repository|ref|update|paths|paths\.\d+|roles|roles\.\d+)$/.test(field) || SKILL_ROLES.some((r) => field === `instructions.${r.id}`)
}

/** The first refusal about the roles (the whole list or one entry). */
export function rolesError(fieldErrors: Readonly<Record<string, string>>): string | undefined {
  return Object.entries(fieldErrors).find(([f]) => f === 'roles' || /^roles\.\d+$/.test(f))?.[1]
}

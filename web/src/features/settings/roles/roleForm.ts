// What the role editor shows and changes, as plain functions: the order of a
// role's models is the order of the list, a model listed twice is a weight,
// and nothing here reorders, merges or drops a list the user did not touch.
import type { Model } from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'
import { MODES } from '../../onboarding/agentSetup'

/** `ok`: the model exists; `missing`: no profile defines it. A role may use a model that agents may not choose for tickets. */
export type ModelMark = 'ok' | 'missing'

export const MARK_TEXT: Record<Exclude<ModelMark, 'ok'>, string> = {
  missing: 'does not exist',
}

const ASSIGNMENT_LABELS: Record<string, string> = {
  round_robin: 'Round robin: each model in turn',
  random: 'Random: a model at random',
  failover: 'Failover: the first that can start',
  smart: 'Smart: the most capacity left',
}

/** The assignment modes in the order they are offered. */
export const ASSIGNMENT_ORDER = ['round_robin', 'random', 'failover', 'smart'] as const

/** The options of the assignment field; a mode written by hand that is not known is kept as it is. */
export function assignmentOptions(current: string): SelectOption[] {
  const known: SelectOption[] = ASSIGNMENT_ORDER.map((value) => ({ value, label: ASSIGNMENT_LABELS[value] ?? value }))
  return current !== '' && !ASSIGNMENT_ORDER.includes(current as (typeof ASSIGNMENT_ORDER)[number]) ? [...known, { value: current, label: current }] : known
}

/** What the mode does, in words. */
export function assignmentMeaning(mode: string): string {
  if (mode === 'smart') return 'Picks the model with the most capacity left when its profile reports usage; otherwise like round robin.'
  const text = MODES[mode]
  return text === undefined ? '' : `How it picks: ${text}.`
}

/** How many times `id` is listed. */
export function weightOf(models: readonly string[], id: string): number {
  let n = 0
  for (const m of models) if (m === id) n++
  return n
}

/** "×2" for a model listed more than once, "" otherwise. */
export function weightText(models: readonly string[], id: string): string {
  const n = weightOf(models, id)
  return n > 1 ? `×${n}` : ''
}

/** The model's mark; `known` null (models not read yet) marks nothing. */
export function markOf(id: string, known: ReadonlyMap<string, Model> | null): ModelMark {
  return known === null || known.has(id) ? 'ok' : 'missing'
}

export function modelIndex(models: readonly Model[] | null): Map<string, Model> | null {
  return models === null ? null : new Map(models.map((m) => [m.id, m]))
}

/** The list as it is, every entry in its place. */
export function rows(models: readonly string[], known: ReadonlyMap<string, Model> | null): { id: string; label: string; note: string }[] {
  return models.map((id) => ({ id, label: id, note: known?.get(id)?.description ?? '' }))
}

/** The options of "Add model": every model, those that agents may not choose for tickets marked. */
export function addOptions(models: readonly Model[]): SelectOption[] {
  return [...models]
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .map((m) => ({ value: m.id, label: m.selectable ? m.id : `${m.id} (agents cannot choose it for tickets)` }))
}

export function addModel(models: readonly string[], id: string): string[] {
  return [...models, id]
}

export function removeModel(models: readonly string[], index: number): string[] {
  return models.filter((_, i) => i !== index)
}

export function moveModel(models: readonly string[], from: number, to: number): string[] {
  const out = [...models]
  if (from < 0 || from >= out.length || to < 0 || to >= out.length || from === to) return out
  const [item] = out.splice(from, 1)
  out.splice(to, 0, item as string)
  return out
}

/** The index a refusal names (`models.2`), or -1. */
export function modelErrorIndex(field: string): number {
  const m = /^models\.(\d+)$/.exec(field)
  return m ? Number(m[1]) : -1
}

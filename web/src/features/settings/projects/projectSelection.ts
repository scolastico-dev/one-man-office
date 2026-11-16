// The parts of the project list's filter and multi-select that are not
// drawing: which projects a filter text shows, the range between two rows,
// and the words for a selection and for the answer of a batch.
import type { Project, ProjectBatchAction } from '../../../api/types'

/** Whether `p` matches every word of `query` in its name or folder, ignoring case. */
export function matchesFilter(p: Pick<Project, 'name' | 'path'>, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return true
  const text = `${p.name}\n${p.path}`.toLowerCase()
  return words.every((w) => text.includes(w))
}

/** The ids from `from` to `to` in `order`, both included; just `to` when `from` is not in it. */
export function rangeIds(order: readonly string[], from: string | null, to: string): string[] {
  const b = order.indexOf(to)
  if (b < 0) return []
  const a = from === null ? -1 : order.indexOf(from)
  if (a < 0) return [to]
  return order.slice(Math.min(a, b), Math.max(a, b) + 1)
}

/** "3 selected", "3 selected, 1 not shown" (hidden by the filter), "None selected". */
export function selectionText(selected: number, hidden: number): string {
  if (selected === 0) return 'None selected'
  return hidden > 0 ? `${selected} selected, ${hidden} not shown` : `${selected} selected`
}

const DONE: Record<ProjectBatchAction, [string, string]> = {
  remove: ['Removed', 'could not be removed'],
  disable: ['Switched off', 'could not be switched off'],
  enable: ['Switched on', 'could not be switched on'],
}

/** "Removed 52; 3 could not be removed." */
export function batchSummary(action: ProjectBatchAction, done: number, failed: number): string {
  const [did, not] = DONE[action]
  if (failed === 0) return `${did} ${done}.`
  if (done === 0) return `${failed === 1 ? 'The project' : `All ${failed}`} ${not}.`
  return `${did} ${done}; ${failed} ${not}.`
}

/** The words of an action's button and of its question, by count. */
export function projectsWord(n: number): string {
  return n === 1 ? '1 project' : `${n} projects`
}

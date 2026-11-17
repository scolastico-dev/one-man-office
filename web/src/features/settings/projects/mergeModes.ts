// The three kinds of merge omo does and the words for their modes, shared by
// the project's own modes (Projects page) and the general defaults (General
// page).
import type { MergeMode, MergeModes, Project } from '../../../api/types'

export type MergeKind = keyof MergeModes

export interface MergeKindDef {
  kind: MergeKind
  label: string
  hint: string
}

export const MERGE_KINDS: readonly MergeKindDef[] = [
  { kind: 'story', label: 'When a story is merged', hint: "A finished story's branch goes into its base branch." },
  { kind: 'task', label: 'When a task without a story is merged', hint: "A finished task's branch goes into its base branch." },
  { kind: 'story_task', label: 'When a task is merged into its story', hint: "A finished task's branch goes into its story's branch." },
]

export const MODE_LABEL: Record<MergeMode, string> = {
  merge: 'Merge commit',
  squash: 'Squash into one commit',
}

/** The options of a general default: a mode. */
export const MODE_OPTIONS = (Object.keys(MODE_LABEL) as MergeMode[]).map((value) => ({ value, label: MODE_LABEL[value] }))

/** The words of a mode; an unknown value is shown as it is. */
export function modeWords(mode: string): string {
  return MODE_LABEL[mode as MergeMode] ?? mode
}

/**
 * The general default of `kind` for a project: the project's effective mode
 * while it has no mode of its own, else the general defaults as last read;
 * null when neither tells.
 */
export function defaultFor(p: Pick<Project, 'merge' | 'effective_merge'>, kind: MergeKind, defaults: MergeModes | null): MergeMode | null {
  if (p.merge[kind] === '') return p.effective_merge[kind]
  return defaults?.[kind] ?? null
}

/** The options of a project's own mode: the general default first, then the modes. */
export function projectModeOptions(p: Pick<Project, 'merge' | 'effective_merge'>, kind: MergeKind, defaults: MergeModes | null) {
  const d = defaultFor(p, kind, defaults)
  return [{ value: '', label: d === null ? 'Use global default' : `Use global default (${modeWords(d)})` }, ...MODE_OPTIONS]
}

/** Whether the project has a mode of its own for any kind. */
export function overridesMerge(p: Pick<Project, 'merge'>): boolean {
  return MERGE_KINDS.some(({ kind }) => p.merge[kind] !== '')
}

/** "Story merges: Squash into one commit" lines for the kinds the project sets itself. */
export function overrideText(p: Pick<Project, 'merge'>): string {
  const names: Record<MergeKind, string> = { story: 'stories', task: 'tasks without a story', story_task: 'tasks into their story' }
  return MERGE_KINDS.filter(({ kind }) => p.merge[kind] !== '')
    .map(({ kind }) => `${names[kind]}: ${modeWords(p.merge[kind])}`)
    .join('; ')
}

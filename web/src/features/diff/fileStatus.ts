import type { DiffFileStatus } from '../../api/types'

/** A file's status as git's letter and in words (the words are what a screen reader says). */
export const FILE_STATUS: Record<DiffFileStatus, readonly [letter: string, words: string]> = {
  added: ['A', 'added'],
  modified: ['M', 'modified'],
  deleted: ['D', 'deleted'],
  renamed: ['R', 'renamed'],
  copied: ['C', 'copied'],
  type_changed: ['T', 'type changed'],
  untracked: ['?', 'untracked'],
}

/** The letter and words of a status, also for one this app does not know yet. */
export function fileStatus(s: DiffFileStatus): readonly [string, string] {
  return FILE_STATUS[s] ?? ['?', s]
}

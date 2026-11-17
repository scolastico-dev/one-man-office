import type { Project } from '../../api/types'

/**
 * The branch a ticket on this project would be based on, when it is not the
 * repository's default branch. Null when there is nothing to say: the default
 * branch is unknown, the branches are equal, or the current branch is unknown
 * (a detached HEAD or an unreadable folder looks the same on the wire).
 */
export function branchNotice(p: Pick<Project, 'current_branch' | 'default_branch'>): { current: string; default: string } | null {
  const current = p.current_branch ?? ''
  const base = p.default_branch ?? ''
  if (current === '' || base === '' || current === base) return null
  return { current, default: base }
}

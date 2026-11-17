// Taking a project off a ticket that has a branch there (the server's
// POST /api/tickets/{n}/remove-project): when the ticket allows it, and what
// a refusal that would delete work lists.
import { ApiError } from '../../api/client'
import type { ProjectWork, TicketProject, TicketSummary } from '../../api/types'

/**
 * Whether the browser offers "Remove from ticket" for project p of t: omo made
 * a branch or worktree there, the ticket keeps another project, and the ticket
 * is in Planning, Ready For Dev or waits for the user in Review. The server
 * checks again (open tasks of a story, working agents).
 */
export function canRemoveProject(t: TicketSummary, p: TicketProject): boolean {
  if (t.projects.length < 2 || (p.branch === '' && p.worktree === '')) return false
  if (t.busy) return false
  switch (t.column) {
    case 'planning':
    case 'ready':
      return true
    case 'review':
      return t.review_stage === 'human'
    default:
      return false
  }
}

/** The work a refused removal would have deleted, or null for any other error. */
export function workOf(err: unknown): ProjectWork | null {
  if (!(err instanceof ApiError) || err.code !== 'conflict') return null
  const d = err.details as Partial<ProjectWork> | undefined
  if (!d || !Array.isArray(d.commits) || !Array.isArray(d.files)) return null
  if (d.commits.length === 0 && d.files.length === 0) return null
  return {
    project: String(d.project ?? ''),
    branch: String(d.branch ?? ''),
    base: String(d.base ?? ''),
    commits: d.commits.map(String),
    files: d.files.map(String),
  }
}

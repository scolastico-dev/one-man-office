// What the export of a ticket does and where it stands per project, in
// words. omo exports project by project: a project whose branch has no
// commits of its own is left out, every other one is pushed and gets its
// pull request, and a project that cannot be exported does not stop the
// others. When one failed, the ticket stays in Review with attention naming
// each failed project ("Export of <project> failed: <reason>", joined by
// "; "); the projects exported keep their pull request, exporting again
// handles only the others, and "Merge locally instead" merges only those.
import type { TicketSummary } from '../../api/types'

const LINK = /^https?:\/\//iu

/** The pull request address of a project, when it is one that may be linked. */
export const linkOf = (url: string | undefined) => (url !== undefined && LINK.test(url) ? url : '')

/** "a", "a and b", "a, b and c" */
function list(parts: readonly string[]): string {
  return parts.length < 2 ? (parts[0] ?? '') : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

/** The failed projects named in the ticket's attention, with their reasons, by project id. */
export function failedExports(attention: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const part of attention.split(/; (?=Export of \S+ failed: )/u)) {
    const m = /^Export of (\S+) failed: (.*)$/su.exec(part.trim())
    if (m) out.set(m[1]!, m[2]!)
  }
  return out
}

export type ProjectExport =
  | { kind: 'pull_request'; url: string }
  | { kind: 'exporting' }
  | { kind: 'failed'; reason: string }
  | { kind: 'left_out' }
  | { kind: 'none' }

/**
 * Where the export of one project of the ticket stands; null when the
 * ticket's export says nothing about it (never exported, or merged).
 */
export function projectExport(t: TicketSummary, project: string): ProjectExport | null {
  const p = t.projects.find((x) => x.project === project)
  if (!p) return null
  if (p.export_url) return { kind: 'pull_request', url: p.export_url }
  if (t.column === 'review' && t.review_stage === 'exporting') return { kind: 'exporting' }
  // Done and exported: every project without a pull request had nothing to export.
  if (t.column === 'done' && t.resolution === 'exported') return { kind: 'left_out' }
  if (t.column !== 'review') return null
  const reason = failedExports(t.attention).get(project)
  if (reason !== undefined) return { kind: 'failed', reason }
  // After a failed export: a project neither exported nor failed was left out or not exported yet.
  if (failedExports(t.attention).size > 0 || t.projects.some((x) => x.export_url)) return { kind: 'none' }
  return null
}

/** The state of a project's export in words, beside the project. */
export function projectExportWords(s: ProjectExport): string {
  switch (s.kind) {
    case 'pull_request':
      return 'Pull request'
    case 'exporting':
      return 'being exported…'
    case 'failed':
      return `export failed: ${s.reason}`
    case 'left_out':
      return 'left out: nothing to export'
    case 'none':
      return 'no pull request'
  }
}

/** The projects of the ticket that have a pull request already. */
const exported = (t: TicketSummary) => t.projects.filter((p) => p.export_url)

/** What exporting the ticket does, for the approve and export dialogs and the action's hint. */
export function exportEffect(t: TicketSummary, nameOf: (id: string) => string): string {
  const done = exported(t)
  if (t.projects.length <= 1) {
    return 'omo pushes the branch and opens a pull request; a branch without commits of its own has nothing to export. The ticket is done once the pull request exists.'
  }
  if (done.length > 0) {
    const rest = t.projects.filter((p) => !p.export_url).map((p) => nameOf(p.project))
    return (
      `omo exports only the projects without a pull request (${list(rest)}); ${list(done.map((p) => nameOf(p.project)))} ${done.length === 1 ? 'keeps its' : 'keep theirs'}. ` +
      'A project with nothing to export is left out. The ticket is done once every other project has its pull request.'
    )
  }
  return (
    'omo exports each project on its own: it pushes the branch and opens a pull request for it, and leaves out a project with nothing to export. ' +
    'A project that cannot be exported does not stop the others; the ticket then stays in Review naming it. The ticket is done once every other project has its pull request.'
  )
}

/** What "Merge locally instead" merges when some projects have a pull request already; empty otherwise. */
export function mergeInsteadNote(t: TicketSummary, nameOf: (id: string) => string): string {
  const done = exported(t)
  if (done.length === 0) return ''
  const rest = t.projects.filter((p) => !p.export_url).map((p) => nameOf(p.project))
  return `Merge locally instead merges only the projects without a pull request (${list(rest)}); ${list(done.map((p) => nameOf(p.project)))} ${done.length === 1 ? 'keeps its pull request' : 'keep their pull requests'}.`
}

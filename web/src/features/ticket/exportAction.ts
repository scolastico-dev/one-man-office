// The Export… action: approves a review that waits for the user and pushes
// the branch for a pull request, whatever the ticket's "when approved"
// setting says. The server decides whether it can be done; the dialog shows
// its refusal.
import { registerTicketAction, type TicketAction } from './actions'
import { withoutProject } from './fieldRules'

export const exportAction: TicketAction = {
  id: 'export',
  label: 'Export…',
  // A task inside a story is merged into the story and cannot be exported; a task without a project has nothing to export.
  applies: (t) => t.column === 'review' && t.review_stage === 'human' && t.story === null && !withoutProject(t),
  unavailable: (t, ctx) => {
    const target = ctx.targets.done
    return target.allowed ? undefined : target.reason ?? `#${t.number} cannot be approved right now.`
  },
  describe: (t) =>
    t.projects.some((p) => p.export_url)
      ? 'Approves it and has omo export the projects without a pull request; those with one keep it.'
      : 'Approves it and has omo push the branch of each project with changes and open a pull request for it.',
  run: (_t, ctx) => ctx.ask('export'),
}

let unregister: (() => void) | null = null

/** Adds Export… to every ticket panel; calling it again changes nothing. */
export function registerExportAction(): void {
  if (unregister === null) unregister = registerTicketAction(exportAction)
}

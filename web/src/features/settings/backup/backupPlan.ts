// Pure helpers of the Backup page: the zip's file name, the import plan in
// groups, the result in sentences.
import type { ImportPlan, ImportResult } from '../../../api/types'

/** The name a settings zip made on `now` is saved as (the server's own rule: the local date). */
export function exportFileName(now: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `omo-settings-${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}.zip`
}

/** The plan's settings files by what the import does to them. */
export interface PlanGroups {
  replace: string[]
  add: string[]
  same: number
}

export function planGroups(plan: ImportPlan): PlanGroups {
  const g: PlanGroups = { replace: [], add: [], same: 0 }
  for (const f of plan.files) {
    if (f.change === 'replace') g.replace.push(f.path)
    else if (f.change === 'add') g.add.push(f.path)
    else g.same++
  }
  return g
}

/** Whether applying the plan with these choices changes anything. */
export function changesAnything(plan: ImportPlan, server: boolean, database: boolean): boolean {
  const g = planGroups(plan)
  return g.replace.length + g.add.length > 0 || (server && plan.server !== '' && plan.server !== 'same') || (database && plan.database)
}

/** "1 agent runs" / "3 agents run". */
export function agentsRunText(n: number): string {
  return n === 1 ? '1 agent runs' : `${n} agents run`
}

/** What the import did, in sentences for the page and the announcer. */
export function resultLines(r: ImportResult): string[] {
  const out: string[] = []
  const n = r.written.length
  out.push(n === 0 ? 'No settings file changed.' : n === 1 ? 'Imported 1 file.' : `Imported ${n} files.`)
  if (r.backup !== '') out.push(`The replaced files are kept in ${r.backup}.`)
  if (r.database_pending) {
    out.push('omo is paused. Restart omo to put the imported database in place (omo stop, then omo); the current database is then kept in a folder below backups.')
  } else if (r.restart_needed) {
    out.push('The server settings apply at the next start of omo.')
  }
  return out
}

import type { PluginButton, PluginsResponse, TicketSummary } from '../../api/types'
import { enabledPlugins, type OwnedButton } from './frontendRuntime'

export interface PluginControl {
  id: string
  plugin: string
  label: string
  action?: string
  ticket?: number
  project?: string
  why?: string
  scripted?: OwnedButton
}
/** Pure expansion shared by ticket panel/page, sidebar and project rows. */
export function controlsOf(list: PluginsResponse | null, scripted: OwnedButton[], place: PluginButton['place'], ticket?: TicketSummary, project?: string): PluginControl[] {
  const all: (PluginButton & { id: string; plugin: string; scripted?: OwnedButton })[] = []
  for (const plugin of enabledPlugins(list)) {
    for (const button of plugin.buttons ?? []) if (button.place === place) all.push({ ...button, id: `${plugin.name}:${place}:${button.action}`, plugin: plugin.name })
  }
  for (const button of scripted) if (button.place === place && button.active()) all.push({ ...button, action: button.action ?? '', id: `${button.plugin}:script:${button.id}`, scripted: button })
  return all.flatMap((b): PluginControl[] => {
    const base: PluginControl = { id: b.id, plugin: b.plugin, label: b.label, action: b.action, ...(b.scripted ? { scripted: b.scripted } : {}), ...(place === 'ticket' ? { ticket: ticket?.number } : {}), ...(project ? { project } : {}) }
    if (!b.per_worktree || place !== 'ticket' || !ticket) return [base]
    const worktrees = ticket.projects.filter((p) => p.worktree)
    if (worktrees.length) return worktrees.map((p) => ({ ...base, id: `${base.id}:${p.project}`, label: `${base.label} — ${p.project}`, project: p.project }))
    if (!ticket.projects.length && ticket.workdir) return [base]
    return [{ ...base, why: 'This ticket has no available working folder. Start work to create its folder first.' }]
  })
}

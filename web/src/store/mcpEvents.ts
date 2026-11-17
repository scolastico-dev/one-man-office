// Which events may change the lists the MCP settings and the first steps
// read from omo (see liveRead).
import type { ApiEvent, SettingsChangedData } from '../api/types'

const changedSection = (ev: ApiEvent): string | undefined =>
  ev.kind === 'settings.changed' ? (ev.data as Partial<SettingsChangedData> | null)?.section : undefined

/**
 * Whether an event may change the agent programs' registration list: the
 * profiles name the account folders, the general settings switch the MCP
 * endpoint.
 */
export function concernsClients(ev: ApiEvent): boolean {
  const section = changedSection(ev)
  return section === 'profiles' || section === 'general'
}

/** Whether an event may change what can be taken over: omo's own servers (a name taken) or the account folders. */
export function concernsTakeover(ev: ApiEvent): boolean {
  const section = changedSection(ev)
  return section === 'mcpservers' || section === 'profiles'
}

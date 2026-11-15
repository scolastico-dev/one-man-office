import type { Project } from '../../api/types'

export type ProjectState = 'enabled' | 'disabled' | 'missing'

/** A project whose folder is gone is `missing` even when it is also switched off. */
export function projectState(p: Pick<Project, 'enabled' | 'missing'>): ProjectState {
  if (p.missing) return 'missing'
  return p.enabled ? 'enabled' : 'disabled'
}

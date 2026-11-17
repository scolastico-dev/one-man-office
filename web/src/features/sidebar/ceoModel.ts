// What the CEO tab shows and offers about the CEO's model. The CEO's model is
// the CEO role's model list (Settings, Roles, the file roles/ceo.yaml): the
// tab reads and writes that one setting, it keeps no choice of its own. A
// choice here makes the chosen model the list's only entry; a running CEO
// keeps its model until its next start.
import type { AgentSession, Model, RoleSettings } from '../../api/types'
import type { PickItem } from '../../components/ui/searchPick'

/** The words for the CEO's model list on the picker's button. */
export function listText(models: readonly string[]): string {
  if (models.length === 0) return 'none'
  const distinct = [...new Set(models)]
  return distinct.length === 1 ? distinct[0]! : `${distinct.length} models`
}

/** Every model, sorted by id: a role may use one that agents may not choose for tickets. The list's only model is marked. */
export function modelItems(models: readonly Model[], list: readonly string[]): PickItem[] {
  const only = new Set(list).size === 1 ? list[0] : undefined
  return [...models]
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .map((m) => ({
      id: m.id,
      label: m.id === only ? `${m.id} (in use)` : m.id,
      ...(m.description ? { detail: m.description } : {}),
    }))
}

/** The CEO role's settings with `id` as its only model, the assignment kept. */
export function withOnlyModel(role: RoleSettings, id: string): RoleSettings {
  return { name: role.name, models: [id], assignment: role.assignment }
}

/** Whether the running CEO runs a model its list no longer names: the list takes effect at its next start. */
export function startsWithOther(live: AgentSession | null, list: readonly string[]): boolean {
  return live !== null && list.length > 0 && !list.includes(live.model)
}
